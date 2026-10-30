package clienttest_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// relationships is the slice of *client.Client a typical consumer depends on.
type relationships interface {
	Write(context.Context, client.WriteRequest, ...client.WriteOption) (client.WriteResult, error)
	ReadRelationships(context.Context, client.ReadRelationshipsRequest, ...client.ReadRelationshipsOption) iter.Seq2[client.ReadRelationship, error]
	DeleteRelationships(context.Context, client.DeleteRequest, ...client.DeleteOption) (client.DeleteResult, error)
	Check(context.Context, client.PermissionCheck, ...client.CheckOption) (client.CheckResult, error)
	CheckBulk(context.Context, []client.PermissionCheck, ...client.CheckOption) ([]client.CheckResult, error)
	ImportRelationships(context.Context, client.ImportRelationshipsRequest, ...client.ImportRelationshipsOption) (client.ImportRelationshipsResult, error)
	ExportRelationships(context.Context, client.ExportRelationshipsRequest, ...client.ExportRelationshipsOption) iter.Seq2[client.ExportedRelationship, error]
	ReadSchema(context.Context, client.ReadSchemaRequest, ...client.ReadSchemaOption) (client.ReadSchemaResult, error)
	WriteSchema(context.Context, client.WriteSchemaRequest, ...client.WriteSchemaOption) (client.WriteSchemaResult, error)
	ReflectSchema(context.Context, client.ReflectSchemaRequest, ...client.ReflectSchemaOption) (client.ReflectSchemaResult, error)
}

var (
	_ relationships = (*client.Client)(nil)
	_ relationships = (*clienttest.Store)(nil)
)

func write(t *testing.T, s relationships, build func(*rel.Txn) error) error {
	t.Helper()
	var txn rel.Txn
	require.NoError(t, build(&txn))
	_, err := s.Write(context.Background(), client.WriteRequest{Transaction: txn})
	return err
}

func TestStoreWriteSemantics(t *testing.T) {
	s := clienttest.NewStore(rel.MustFromTuple("org:acme#admin@user:alice"))

	err := write(t, s, func(txn *rel.Txn) error { return txn.Create(rel.MustFromTuple("org:acme#admin@user:alice")) })
	require.ErrorIs(t, err, client.ErrAlreadyExists)
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	// MustMatch fails atomically: the touch is not applied.
	err = write(t, s, func(txn *rel.Txn) error {
		if err := txn.MustMatch(rel.MustNewFilter("org").WithRelation("owner")); err != nil {
			return err
		}
		return txn.Touch(rel.MustFromTuple("org:acme#member@user:bob"))
	})
	require.ErrorIs(t, err, client.ErrFailedPrecondition)
	require.Len(t, s.Relationships(), 1)

	err = write(t, s, func(txn *rel.Txn) error {
		if err := txn.MustNotMatch(rel.MustNewFilter("org").WithRelation("member")); err != nil {
			return err
		}
		if err := txn.Delete(rel.MustFromTuple("org:acme#admin@user:alice")); err != nil {
			return err
		}
		return txn.Touch(rel.MustFromTuple("org:acme#member@user:alice"))
	})
	require.NoError(t, err)

	var got []string
	for r, err := range s.ReadRelationships(context.Background(), client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("org").WithResourceID("acme")}) {
		require.NoError(t, err)
		got = append(got, r.String())
	}
	require.Equal(t, []string{"org:acme#member@user:alice"}, got)

	deleted, err := s.DeleteRelationships(context.Background(), client.DeleteRequest{Filter: rel.MustNewFilter("org"), Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted.DeletedCount)
	require.True(t, deleted.Complete)
}

func TestStoreCheckResults(t *testing.T) {
	s := clienttest.NewStore()
	doc := rel.MustNewObject("document", "readme")
	alice := rel.MustNewSubject(rel.MustNewObject("user", "alice"), "")
	bob := rel.MustNewSubject(rel.MustNewObject("user", "bob"), "")
	s.SetCheckResult(doc, "view", alice, client.CheckResult{Permissionship: client.PermissionshipHasPermission})

	results, err := s.CheckBulk(context.Background(), []client.PermissionCheck{
		{Resource: doc, Permission: "view", Subject: bob, Consistency: consistency.Full()},
		{Resource: doc, Permission: "view", Subject: alice, Consistency: consistency.Full()},
	})
	require.NoError(t, err)
	require.False(t, results[0].HasPermission())
	require.True(t, results[1].HasPermission())
}

func TestStoreErrorsMatchTheRealClientsShape(t *testing.T) {
	s := clienttest.NewStore(rel.MustFromTuple("org:acme#admin@user:alice"))
	err := write(t, s, func(txn *rel.Txn) error { return txn.MustMatch(rel.MustNewFilter("org").WithRelation("owner")) })
	var e *client.Error
	require.ErrorAs(t, err, &e)
	require.Regexp(t, "^spicedb: write: unable to satisfy write precondition `", e.Message)
	require.Equal(t, strings.TrimPrefix(e.Message, "spicedb: write: "), e.ServerMessage)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Local validation looks like the client's local validation.
	for _, err := range s.ReadRelationships(context.Background(), client.ReadRelationshipsRequest{Filter: rel.Filter{}}) {
		require.ErrorIs(t, err, client.ErrInvalidArgument)
		require.Regexp(t, "^spicedb: filter: ", err.Error())
	}

	// An empty write checks preconditions and returns the revision.
	result, err := s.Write(context.Background(), client.WriteRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, result.Revision.String())
}

func TestStoreBulkAndSchemaOperations(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore()
	tuples := []string{"doc:a#viewer@user:x", "doc:b#viewer@user:x", "doc:c#viewer@user:x", "folder:f#viewer@user:x"}
	imported, err := s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: func(yield func(rel.Relationship) bool) {
		for _, tuple := range tuples {
			if !yield(rel.MustFromTuple(tuple)) {
				return
			}
		}
	}})
	require.NoError(t, err)
	require.EqualValues(t, 4, imported.ImportedCount)

	_, err = s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: func(yield func(rel.Relationship) bool) {
		yield(rel.MustFromTuple("doc:z#viewer@user:x"))
		yield(rel.MustFromTuple("doc:a#viewer@user:x"))
	}})
	require.ErrorIs(t, err, client.ErrAlreadyExists)
	require.Len(t, s.Relationships(), 4, "a failed import adds nothing")

	// Cursors resume exactly where a limited read stopped.
	var first []client.ReadRelationship
	for r, err := range s.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("doc"), Consistency: consistency.Full()}, client.WithReadRelationshipsLimit(2)) {
		require.NoError(t, err)
		first = append(first, r)
	}
	require.Len(t, first, 2)
	var rest []string
	for r, err := range s.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("doc"), Consistency: consistency.Full()}, client.WithReadRelationshipsCursor(first[1].AfterResultCursor)) {
		require.NoError(t, err)
		rest = append(rest, r.ResourceID())
	}
	require.Equal(t, []string{"c"}, rest)

	var exported []string
	for r, err := range s.ExportRelationships(ctx, client.ExportRelationshipsRequest{Consistency: consistency.Full()}, client.WithExportRelationshipsFilter(rel.MustNewFilter("folder"))) {
		require.NoError(t, err)
		exported = append(exported, r.String())
	}
	require.Equal(t, []string{"folder:f#viewer@user:x"}, exported)

	_, err = s.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.ErrorIs(t, err, client.ErrNotFound)
	_, err = s.WriteSchema(ctx, client.WriteSchemaRequest{Schema: "definition user {}"})
	require.NoError(t, err)
	schema, err := s.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.NoError(t, err)
	require.Equal(t, "definition user {}", schema.Schema)

	s.SetReflectSchemaResult(client.ReflectSchemaResult{Definitions: []client.SchemaDefinition{{Name: "user"}}})
	reflected, err := s.ReflectSchema(ctx, client.ReflectSchemaRequest{Consistency: consistency.MinLatency()})
	require.NoError(t, err)
	require.Equal(t, "user", reflected.Definitions[0].Name)
}

func TestStoreFillsResponseMetadata(t *testing.T) {
	s := clienttest.NewStore()
	s.SetResponseMetadata(metadata.Pairs("io.spicedb.respmeta.version", "v1.99"), metadata.Pairs("t", "1"))
	_, err := s.WriteSchema(context.Background(), client.WriteSchemaRequest{Schema: "definition user {}"})
	require.NoError(t, err)
	var header, trailer metadata.MD
	_, err = s.ReadSchema(context.Background(), client.ReadSchemaRequest{}, client.WithResponseMetadata[client.ReadSchemaOption](&header, &trailer))
	require.NoError(t, err)
	require.Equal(t, []string{"v1.99"}, header.Get("io.spicedb.respmeta.version"))
	require.Equal(t, []string{"1"}, trailer.Get("t"))
}

func TestStoreMatchesServerEdgeCases(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore(rel.MustFromTuple("doc:a#viewer@user:x"), rel.MustFromTuple("doc:b#viewer@user:x"))

	_, err := s.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.ErrorIs(t, err, client.ErrNotFound)
	require.Equal(t, "spicedb: read schema: no schema has been defined; please call WriteSchema to start", err.Error())

	reflected, err := s.ReflectSchema(ctx, client.ReflectSchemaRequest{Consistency: consistency.MinLatency()})
	require.NoError(t, err, "reflecting an empty schema succeeds")
	require.Empty(t, reflected.Definitions)

	_, err = s.DeleteRelationships(ctx, client.DeleteRequest{Filter: rel.MustNewFilter("doc"), Limit: 1}, client.WithDeletePartial(client.PartialDisallowed))
	var e *client.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, client.CodeInvalidArgument, e.Code)
	require.Equal(t, "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE", e.Reason)
}

func tuples(n int) iter.Seq[rel.Relationship] {
	return func(yield func(rel.Relationship) bool) {
		for i := 0; i < n; i++ {
			if !yield(rel.MustFromTuple(fmt.Sprintf("doc:d%d#viewer@user:x", i))) {
				return
			}
		}
	}
}

func TestStoreFaultInjection(t *testing.T) {
	ctx := context.Background()

	// A restore test: the third import batch fails with AlreadyExists, and nothing is applied.
	s := clienttest.NewStore()
	s.SetFaultHook(func(f clienttest.Fault) error {
		if f.Call.Operation == clienttest.OpImportRelationships && f.Stage == clienttest.StageImportBatch && f.Item == 2 {
			return clienttest.ServerError(clienttest.OpImportRelationships, client.CodeAlreadyExists, "duplicate relationship")
		}
		return nil
	})
	_, err := s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: tuples(10)}, client.WithImportRelationshipsBatchSize(3))
	require.ErrorIs(t, err, client.ErrAlreadyExists)
	require.Equal(t, "spicedb: import relationships: duplicate relationship", err.Error())
	require.Empty(t, s.Relationships())

	// The commit itself can fail.
	s.SetFaultHook(func(f clienttest.Fault) error {
		if f.Stage == clienttest.StageImportCommit {
			return clienttest.ServerError(clienttest.OpImportRelationships, client.CodeUnavailable, "lost")
		}
		return nil
	})
	_, err = s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: tuples(4)})
	require.ErrorIs(t, err, client.ErrUnavailable)
	require.Empty(t, s.Relationships())

	// A backup test: the export fails after K items.
	s.SetFaultHook(nil)
	_, err = s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: tuples(5)})
	require.NoError(t, err)
	s.SetFaultHook(func(f clienttest.Fault) error {
		if f.Call.Operation == clienttest.OpExportRelationships && f.Stage == clienttest.StageItem && f.Item == 3 {
			return clienttest.ServerError(clienttest.OpExportRelationships, client.CodeUnavailable, "stream reset")
		}
		return nil
	})
	var got int
	var streamErr error
	for _, err := range s.ExportRelationships(ctx, client.ExportRelationshipsRequest{Consistency: consistency.Full()}) {
		if err != nil {
			streamErr = err
			break
		}
		got++
	}
	require.Equal(t, 3, got)
	require.ErrorIs(t, streamErr, client.ErrUnavailable)

	// FailNext fails one call of one operation.
	s.FailNext(clienttest.OpCheck, clienttest.ServerError(clienttest.OpCheck, client.CodeUnavailable, "down"))
	check := client.PermissionCheck{Resource: rel.MustNewObject("doc", "d0"), Permission: "view",
		Subject: rel.MustNewSubject(rel.MustNewObject("user", "x"), ""), Consistency: consistency.Full()}
	_, err = s.Check(ctx, check)
	require.ErrorIs(t, err, client.ErrUnavailable)
	_, err = s.Check(ctx, check)
	require.NoError(t, err)
}

func TestStoreRecordsCalls(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore()
	for range s.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("doc"), Consistency: consistency.Full()},
		client.WithReadRelationshipsCursor("2"), client.WithReadRelationshipsLimit(5)) {
	}
	for range s.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("folder"), Consistency: consistency.Full()}) {
	}
	reads := s.CallsOf(clienttest.OpReadRelationships)
	require.Len(t, reads, 2)
	require.Equal(t, 0, reads[0].Index)
	require.Equal(t, 1, reads[1].Index)
	require.Equal(t, "2", reads[0].Options.Cursor)
	require.EqualValues(t, 5, reads[0].Options.Limit)
	require.Equal(t, "folder", reads[1].Request.(client.ReadRelationshipsRequest).Filter.ResourceType())
}

// Both the real client and the Store satisfy the full client interface.
var (
	_ client.Interface = (*client.Client)(nil)
	_ client.Interface = (*clienttest.Store)(nil)
)

func TestStoreCoversTheWholeClient(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore(rel.MustFromTuple("doc:a#viewer@user:x"), rel.MustFromTuple("doc:b#viewer@user:x"))

	// Operations without in-memory semantics fail like the server would.
	_, err := s.ExpandPermissionTree(ctx, client.ExpandPermissionTreeRequest{})
	require.ErrorIs(t, err, client.ErrUnimplemented)
	require.Equal(t, "spicedb: expand permission tree: not implemented by clienttest.Store", err.Error())
	for _, err := range s.Updates(ctx, client.UpdatesRequest{}) {
		require.ErrorIs(t, err, client.ErrUnimplemented)
	}
	require.Len(t, s.CallsOf(clienttest.OpUpdates), 1, "unimplemented calls are still recorded")

	// CheckIter uses the registered results.
	var results []client.CheckResult
	for r, err := range s.CheckIter(ctx, func(yield func(client.PermissionCheck) bool) {
		yield(client.PermissionCheck{Resource: rel.MustNewObject("doc", "a"), Permission: "view",
			Subject: rel.MustNewSubject(rel.MustNewObject("user", "x"), ""), Consistency: consistency.Full()})
	}) {
		require.NoError(t, err)
		results = append(results, r)
	}
	require.Len(t, results, 1)

	// DeleteAll removes every match and reports progress.
	var progress []client.DeleteResult
	deleted, err := s.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: rel.MustNewFilter("doc")},
		client.WithDeleteAllProgress(func(r client.DeleteResult) { progress = append(progress, r) }))
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted.DeletedCount)
	require.True(t, deleted.Complete)
	require.Len(t, progress, 1)
	require.Empty(t, s.Relationships())

	require.NoError(t, s.Close())
	require.Nil(t, s.RawProto())
}

func TestStoreReadSchemaWithoutRevision(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore()
	_, err := s.WriteSchema(ctx, client.WriteSchemaRequest{Schema: "definition user {}"})
	require.NoError(t, err)
	withRevision, err := s.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, withRevision.Revision.String())

	s.SetReadSchemaRevision(clienttest.RevisionOmitted)
	omitted, err := s.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, omitted.Revision.String())
	require.Equal(t, "definition user {}", omitted.Schema)
}

func TestStoreFaultSeesImportBatchContents(t *testing.T) {
	s := clienttest.NewStore()
	var batches [][]string
	s.SetFaultHook(func(f clienttest.Fault) error {
		if f.Stage != clienttest.StageImportBatch {
			return nil
		}
		var ids []string
		for _, r := range f.Batch {
			ids = append(ids, r.ResourceID())
			if r.ResourceID() == "d4" {
				return clienttest.ServerError(clienttest.OpImportRelationships, client.CodeAlreadyExists, "duplicate d4")
			}
		}
		batches = append(batches, ids)
		return nil
	})
	_, err := s.ImportRelationships(context.Background(), client.ImportRelationshipsRequest{Relationships: tuples(7)}, client.WithImportRelationshipsBatchSize(3))
	require.ErrorIs(t, err, client.ErrAlreadyExists)
	require.Equal(t, [][]string{{"d0", "d1", "d2"}}, batches, "the hook saw the first batch, then failed on the one holding d4")
	require.Empty(t, s.Relationships())
}

func TestCursorAfterResumesLikeAStoreCursor(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore()
	_, err := s.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: tuples(5)})
	require.NoError(t, err)
	var cursors []string
	for r, err := range s.ExportRelationships(ctx, client.ExportRelationshipsRequest{Consistency: consistency.Full()}) {
		require.NoError(t, err)
		cursors = append(cursors, r.AfterResultCursor)
	}
	require.Equal(t, cursors[1], clienttest.CursorAfter(2), "CursorAfter(n) is the cursor on the n-th item")
	var rest []string
	for r, err := range s.ExportRelationships(ctx, client.ExportRelationshipsRequest{Consistency: consistency.Full()}, client.WithExportRelationshipsCursor(clienttest.CursorAfter(3))) {
		require.NoError(t, err)
		rest = append(rest, r.ResourceID())
	}
	require.Equal(t, []string{"d3", "d4"}, rest)
}

// Ready on the Store is an accepted no-op, so startup code that calls it runs
// unchanged against the double.
func TestStoreReadyIsNoOp(t *testing.T) {
	s := clienttest.NewStore()
	var iface client.Interface = s
	require.NoError(t, iface.Ready(context.Background(), client.WithReadyTimeout(1)))
	require.Empty(t, s.Calls())
}
