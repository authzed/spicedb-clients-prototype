package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// adoptionServer is a scriptable fake for the native behaviors added for
// zed's migration: retries, deadlines, import aborts, bulk item errors,
// watch kinds and delete modes.
type adoptionServer struct {
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedSchemaServiceServer
	v1.UnimplementedWatchServiceServer

	mu            sync.Mutex
	readSchema    atomic.Int32
	readSchemaErr codes.Code
	watchCalls    atomic.Int32
	watchKinds    []v1.WatchKind
	importBatches []int
	deletes       []*v1.DeleteRelationshipsRequest
	deletePages   []*v1.DeleteRelationshipsResponse
	schemaDelay   time.Duration
	writes        atomic.Int32
	writeErr      codes.Code
	schemaWrites  atomic.Int32
	lookups       []*v1.LookupResourcesRequest
}

func (s *adoptionServer) WriteRelationships(context.Context, *v1.WriteRelationshipsRequest) (*v1.WriteRelationshipsResponse, error) {
	if s.writes.Add(1) == 1 && s.writeErr != codes.OK {
		return nil, status.Error(s.writeErr, "transient")
	}
	return &v1.WriteRelationshipsResponse{WrittenAt: &v1.ZedToken{Token: "w"}}, nil
}

func (s *adoptionServer) WriteSchema(context.Context, *v1.WriteSchemaRequest) (*v1.WriteSchemaResponse, error) {
	if s.schemaWrites.Add(1) == 1 {
		return nil, status.Error(codes.Unavailable, "transient")
	}
	return &v1.WriteSchemaResponse{WrittenAt: &v1.ZedToken{Token: "s"}}, nil
}

func (s *adoptionServer) LookupResources(req *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	s.mu.Lock()
	s.lookups = append(s.lookups, req)
	s.mu.Unlock()
	for i := 0; i < 3; i++ {
		if err := stream.Send(&v1.LookupResourcesResponse{ResourceObjectId: fmt.Sprint(i), Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, AfterResultCursor: &v1.Cursor{Token: fmt.Sprint("c", i)}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *adoptionServer) ReadSchema(ctx context.Context, _ *v1.ReadSchemaRequest) (*v1.ReadSchemaResponse, error) {
	s.readSchema.Add(1)
	if s.schemaDelay > 0 {
		select {
		case <-time.After(s.schemaDelay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if s.readSchemaErr != codes.OK {
		return nil, status.Error(s.readSchemaErr, "nope")
	}
	return &v1.ReadSchemaResponse{SchemaText: "definition user {}", ReadAt: &v1.ZedToken{Token: "r"}}, nil
}

func (s *adoptionServer) Watch(req *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	s.watchCalls.Add(1)
	s.mu.Lock()
	s.watchKinds = req.GetOptionalUpdateKinds()
	s.mu.Unlock()
	if s.watchCalls.Load() == 1 && len(s.watchKinds) == 0 {
		return status.Error(codes.Unavailable, "flaky")
	}
	return stream.Send(&v1.WatchResponse{ChangesThrough: &v1.ZedToken{Token: "w"}, SchemaUpdated: true})
}

func (s *adoptionServer) ImportBulkRelationships(stream grpc.ClientStreamingServer[v1.ImportBulkRelationshipsRequest, v1.ImportBulkRelationshipsResponse]) error {
	var total uint64
	for {
		req, err := stream.Recv()
		if err != nil {
			return stream.SendAndClose(&v1.ImportBulkRelationshipsResponse{NumLoaded: total})
		}
		s.mu.Lock()
		s.importBatches = append(s.importBatches, len(req.GetRelationships()))
		s.mu.Unlock()
		for _, r := range req.GetRelationships() {
			if r.GetResource().GetObjectId() == "dup" {
				return status.Error(codes.AlreadyExists, "duplicate relationship")
			}
		}
		total += uint64(len(req.GetRelationships()))
	}
}

func (s *adoptionServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	resp := &v1.CheckBulkPermissionsResponse{CheckedAt: &v1.ZedToken{Token: "c"}}
	for _, item := range req.GetItems() {
		pair := &v1.CheckBulkPermissionsPair{Request: item}
		if item.GetResource().GetObjectId() == "broken" {
			pair.Response = &v1.CheckBulkPermissionsPair_Error{Error: &rpcstatus.Status{Code: int32(codes.FailedPrecondition), Message: "no such permission"}}
		} else {
			pair.Response = &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}}
		}
		resp.Pairs = append(resp.Pairs, pair)
	}
	return resp, nil
}

func (s *adoptionServer) DeleteRelationships(_ context.Context, req *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, req)
	if len(s.deletePages) > 0 {
		page := s.deletePages[0]
		s.deletePages = s.deletePages[1:]
		return page, nil
	}
	return &v1.DeleteRelationshipsResponse{DeletedAt: &v1.ZedToken{Token: "d"}, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE}, nil
}

func newAdoptionClient(t *testing.T, srv *adoptionServer, opts ...Option) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(g, srv)
	v1.RegisterSchemaServiceServer(g, srv)
	v1.RegisterWatchServiceServer(g, srv)
	go func() { _ = g.Serve(listener) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	c, err := NewFromConn(conn, opts...)
	require.NoError(t, err)
	return c
}

func TestImportReportsServerAbortNotEOF(t *testing.T) {
	c := newAdoptionClient(t, &adoptionServer{})
	rels := func(yield func(rel.Relationship) bool) {
		// Enough batches that a later Send observes the aborted stream.
		for i := 0; i < 50; i++ {
			id := "ok"
			if i == 0 {
				id = "dup"
			}
			if !yield(testTriple("document", id, "viewer", "user", "alice", "")) {
				return
			}
		}
	}
	_, err := c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(1))
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestImportBatchSize(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	rels := func(yield func(rel.Relationship) bool) {
		for i := 0; i < 7; i++ {
			if !yield(testTriple("document", "ok", "viewer", "user", "alice", "")) {
				return
			}
		}
	}
	result, err := c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(3))
	require.NoError(t, err)
	require.EqualValues(t, 7, result.ImportedCount)
	require.Equal(t, []int{3, 3, 1}, srv.importBatches)
	_, err = c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(0))
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestRetryPolicyIsConfigurable(t *testing.T) {
	ctx := context.Background()

	srv := &adoptionServer{readSchemaErr: codes.Unavailable}
	_, err := newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 1, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond})).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrUnavailable)
	require.EqualValues(t, 2, srv.readSchema.Load(), "one attempt plus one retry")

	srv = &adoptionServer{readSchemaErr: codes.Unavailable}
	_, err = newAdoptionClient(t, srv, WithRetries(RetriesDisabled)).ReadSchema(ctx, ReadSchemaRequest{})
	require.Error(t, err)
	require.EqualValues(t, 1, srv.readSchema.Load(), "disabled retries make one attempt")

	var events []RetryEvent
	srv = &adoptionServer{readSchemaErr: codes.Internal}
	_, err = newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 2, RetryableCodes: []ErrorCode{CodeInternal}, InitialBackoff: time.Millisecond,
		OnRetry: func(e RetryEvent) { events = append(events, e) }})).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrInternal)
	require.EqualValues(t, 3, srv.readSchema.Load())
	require.Len(t, events, 2)
	require.Equal(t, "read schema", events[0].Operation)
	require.Equal(t, 1, events[0].Attempt)
	require.ErrorIs(t, events[0].Err, ErrInternal)

	_, err = NewFromConn(&grpc.ClientConn{}, WithRetryPolicy(RetryPolicy{MaxRetries: 1}))
	require.ErrorIs(t, err, ErrInvalidArgument, "retries need a positive backoff")
}

func TestStreamRetriesCanBeDisabled(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 3, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}))
	for _, err := range c.Updates(context.Background(), UpdatesRequest{}) {
		require.NoError(t, err)
		break
	}
	require.EqualValues(t, 2, srv.watchCalls.Load(), "enabled: the failed open is retried")

	srv = &adoptionServer{}
	c = newAdoptionClient(t, srv, WithStreamRetries(StreamRetriesDisabled))
	for _, err := range c.Updates(context.Background(), UpdatesRequest{}) {
		require.ErrorIs(t, err, ErrUnavailable)
		break
	}
	require.EqualValues(t, 1, srv.watchCalls.Load(), "disabled: one open")
}

func TestDefaultDeadlineCanBeDisabled(t *testing.T) {
	srv := &adoptionServer{schemaDelay: 150 * time.Millisecond}
	_, err := newAdoptionClient(t, srv, WithDefaultTimeout(50*time.Millisecond)).ReadSchema(context.Background(), ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrDeadlineExceeded)

	_, err = newAdoptionClient(t, srv, WithDefaultTimeout(50*time.Millisecond), WithDefaultDeadline(DefaultDeadlineDisabled)).ReadSchema(context.Background(), ReadSchemaRequest{})
	require.NoError(t, err, "no client deadline")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = newAdoptionClient(t, srv, WithDefaultDeadline(DefaultDeadlineDisabled)).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded, "the caller's deadline still applies")
}

func TestBulkItemErrorsReturned(t *testing.T) {
	c := newAdoptionClient(t, &adoptionServer{})
	requests := []PermissionCheck{
		testRequest("ok-1"),
		testRequest("broken"),
		{Resource: rel.ObjectRef{}, Permission: "view", Consistency: consistency.Full()}, // invalid locally
		testRequest("ok-2"),
	}

	_, err := c.CheckBulk(context.Background(), requests)
	var itemErr *BulkCheckError
	require.ErrorAs(t, err, &itemErr, "default mode fails the call")

	results, err := c.CheckBulk(context.Background(), requests, WithBulkItemErrors(ItemErrorsReturned))
	require.NoError(t, err)
	require.Len(t, results, 4)
	require.True(t, results[0].HasPermission())
	require.True(t, results[3].HasPermission())
	for i, want := range map[int]error{1: ErrFailedPrecondition, 2: ErrInvalidArgument} {
		require.False(t, results[i].HasPermission(), "an errored item never grants")
		require.ErrorAs(t, results[i].Err, &itemErr)
		require.Equal(t, i, itemErr.Index)
		require.ErrorIs(t, results[i].Err, want)
	}

	var iterResults []CheckResult
	for r, err := range c.CheckIter(context.Background(), func(yield func(PermissionCheck) bool) {
		for _, r := range requests {
			if !yield(r) {
				return
			}
		}
	}, WithBulkItemErrors(ItemErrorsReturned)) {
		require.NoError(t, err)
		iterResults = append(iterResults, r)
	}
	require.Len(t, iterResults, 4)
	for i := range requests {
		require.Equal(t, results[i].HasPermission(), iterResults[i].HasPermission(), "item %d", i)
		require.Equal(t, results[i].Err != nil, iterResults[i].Err != nil, "item %d", i)
	}
}

func TestWatchSchemaChanges(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	for event, err := range c.Updates(context.Background(), UpdatesRequest{}, WithUpdatesSchemaChanges(SchemaChangesIncluded), WithUpdatesCheckpoints(CheckpointsIncluded)) {
		require.NoError(t, err)
		require.True(t, event.SchemaUpdated)
		break
	}
	require.ElementsMatch(t, []v1.WatchKind{
		v1.WatchKind_WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES,
		v1.WatchKind_WATCH_KIND_INCLUDE_CHECKPOINTS,
		v1.WatchKind_WATCH_KIND_INCLUDE_SCHEMA_UPDATES,
	}, srv.watchKinds)
}

func TestAllOrNothingDeleteAndProgress(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	ctx := context.Background()

	_, err := c.DeleteRelationships(ctx, DeleteRequest{Filter: testFilter("document")})
	require.ErrorIs(t, err, ErrInvalidArgument, "partial deletes need a limit")
	_, err = c.DeleteRelationships(ctx, DeleteRequest{Filter: testFilter("document")}, WithDeletePartial(PartialDisallowed))
	require.NoError(t, err)
	last := srv.deletes[len(srv.deletes)-1]
	require.False(t, last.GetOptionalAllowPartialDeletions())
	require.Zero(t, last.GetOptionalLimit(), "zero uses the server default")

	srv.deletePages = []*v1.DeleteRelationshipsResponse{
		{DeletedAt: &v1.ZedToken{Token: "1"}, RelationshipsDeletedCount: 2, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, AfterResultCursor: &v1.Cursor{Token: "k1"}},
		{DeletedAt: &v1.ZedToken{Token: "2"}, RelationshipsDeletedCount: 1, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE},
	}
	var progress []DeleteResult
	result, err := c.DeleteAllRelationshipsNonAtomic(ctx, DeleteAllRequest{Filter: testFilter("document")}, WithDeleteAllProgress(func(r DeleteResult) { progress = append(progress, r) }))
	require.NoError(t, err)
	require.EqualValues(t, 3, result.DeletedCount)
	require.Len(t, progress, 2)
	require.EqualValues(t, 2, progress[0].DeletedCount)
	require.Equal(t, "k1", progress[0].Cursor)
	require.EqualValues(t, 3, progress[1].DeletedCount)
	require.True(t, progress[1].Complete)
}

func TestCallOptionsReachEveryOperation(t *testing.T) {
	// WithCallOptions instantiates for every operation's option type.
	called := 0
	interceptor := grpc.OnFinish(func(error) { called++ })
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	_, err := c.ReadSchema(context.Background(), ReadSchemaRequest{}, WithCallOptions[ReadSchemaOption](interceptor))
	require.NoError(t, err)
	_, err = c.DeleteRelationships(context.Background(), DeleteRequest{Filter: testFilter("document"), Limit: 1}, WithCallOptions[DeleteOption](interceptor))
	require.NoError(t, err)
	require.Equal(t, 2, called)
	require.False(t, errors.Is(err, ErrInvalidArgument))
}

func TestPerCallRetryPolicy(t *testing.T) {
	ctx := context.Background()
	retryOnce := RetryPolicy{MaxRetries: 1, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}
	touch := func() rel.Txn {
		var txn rel.Txn
		require.NoError(t, txn.Touch(testTriple("document", "a", "viewer", "user", "alice", "")))
		return txn
	}

	// Mutations are never retried by default.
	srv := &adoptionServer{writeErr: codes.Unavailable}
	c := newAdoptionClient(t, srv)
	_, err := c.Write(ctx, WriteRequest{Transaction: touch()})
	require.ErrorIs(t, err, ErrUnavailable)
	require.EqualValues(t, 1, srv.writes.Load())

	// A touch-only write may opt in for one call.
	srv = &adoptionServer{writeErr: codes.Unavailable}
	c = newAdoptionClient(t, srv)
	_, err = c.Write(ctx, WriteRequest{Transaction: touch()}, WithCallRetryPolicy[WriteOption](retryOnce))
	require.NoError(t, err)
	require.EqualValues(t, 2, srv.writes.Load())

	// Non-idempotent writes refuse the opt-in before any RPC.
	var create rel.Txn
	require.NoError(t, create.Create(testTriple("document", "a", "viewer", "user", "alice", "")))
	var guarded rel.Txn
	require.NoError(t, guarded.MustMatch(testFilter("document")))
	require.NoError(t, guarded.Touch(testTriple("document", "a", "viewer", "user", "alice", "")))
	for _, txn := range []rel.Txn{create, guarded} {
		before := srv.writes.Load()
		_, err = c.Write(ctx, WriteRequest{Transaction: txn}, WithCallRetryPolicy[WriteOption](retryOnce))
		require.ErrorIs(t, err, ErrInvalidArgument)
		require.Equal(t, before, srv.writes.Load())
	}
	_, err = c.DeleteRelationships(ctx, DeleteRequest{Filter: testFilter("document"), Limit: 1}, WithCallRetryPolicy[DeleteOption](retryOnce))
	require.ErrorIs(t, err, ErrInvalidArgument)

	// Schema writes are idempotent and may opt in.
	_, err = c.WriteSchema(ctx, WriteSchemaRequest{Schema: "definition user {}"}, WithCallRetryPolicy[WriteSchemaOption](retryOnce))
	require.NoError(t, err)
	require.EqualValues(t, 2, srv.schemaWrites.Load())

	// One stream can be exempted while the client keeps retrying others.
	srv = &adoptionServer{}
	c = newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 3, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}))
	for _, err := range c.Updates(ctx, UpdatesRequest{}, WithCallRetryPolicy[UpdatesOption](RetryPolicy{})) {
		require.ErrorIs(t, err, ErrUnavailable)
		break
	}
	require.EqualValues(t, 1, srv.watchCalls.Load())
}

func TestEmptyWriteIsSentToTheServer(t *testing.T) {
	srv := &adoptionServer{}
	result, err := newAdoptionClient(t, srv).Write(context.Background(), WriteRequest{})
	require.NoError(t, err)
	require.Equal(t, "w", result.Revision.String())
	require.EqualValues(t, 1, srv.writes.Load())
}

func TestLookupResourcesSingleRequest(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	req := LookupResourcesRequest{ResourceType: "document", Permission: "view", Subject: testSubject("user", "alice", ""), Consistency: consistency.Full()}
	var cursors []string
	for r, err := range c.LookupResources(context.Background(), req, WithLookupResourcesPaging(PagingSingleRequest)) {
		require.NoError(t, err)
		cursors = append(cursors, r.AfterResultCursor)
	}
	require.Equal(t, []string{"c0", "c1", "c2"}, cursors)
	require.Len(t, srv.lookups, 1, "exactly one request")
	require.Zero(t, srv.lookups[0].GetOptionalLimit(), "no limit option means unbounded")
}

func TestResolveOptions(t *testing.T) {
	var header metadata.MD
	r := ResolveOptions(
		WithReadRelationshipsCursor("k"),
		WithReadRelationshipsLimit(7),
		WithResponseMetadata[ReadRelationshipsOption](&header, nil),
		WithCallRetryPolicy[ReadRelationshipsOption](RetryPolicy{}),
	)
	require.NoError(t, r.Err)
	require.Equal(t, "k", r.Cursor)
	require.EqualValues(t, 7, r.Limit)
	require.EqualValues(t, defaultReadPageSize, r.PageSize, "operation defaults apply")
	require.NotNil(t, r.RetryPolicy)
	h, tr := r.ResponseMetadata()
	require.Same(t, &header, h)
	require.Nil(t, tr)

	require.EqualValues(t, 3, ResolveOptions(WithImportRelationshipsBatchSize(3)).PageSize)
	require.Error(t, ResolveOptions(WithImportRelationshipsBatchSize(0)).Err)
	require.Equal(t, PartialDisallowed, ResolveOptions(WithDeletePartial(PartialDisallowed)).PartialDeletion)
}

// noRetry is the kind of generic helper the exported OperationOption allows.
func noRetry[O OperationOption]() O { return WithCallRetryPolicy[O](RetryPolicy{}) }

func TestOperationRetryPolicy(t *testing.T) {
	ctx := context.Background()
	retryOnce := RetryPolicy{MaxRetries: 1, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}

	// A client that never reopens watches, with no per-call option.
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv,
		WithRetryPolicy(RetryPolicy{MaxRetries: 3, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}),
		WithOperationRetryPolicy[UpdatesOption](RetryPolicy{}))
	for _, err := range c.Updates(ctx, UpdatesRequest{}) {
		require.ErrorIs(t, err, ErrUnavailable)
		break
	}
	require.EqualValues(t, 1, srv.watchCalls.Load())
	// ...while another operation keeps the client-wide policy.
	srv.readSchemaErr = codes.Unavailable
	_, err := c.ReadSchema(ctx, ReadSchemaRequest{})
	require.Error(t, err)
	require.EqualValues(t, 4, srv.readSchema.Load())

	// A per-call option still wins over the operation policy.
	srv = &adoptionServer{}
	c = newAdoptionClient(t, srv, WithOperationRetryPolicy[UpdatesOption](RetryPolicy{}))
	for _, err := range c.Updates(ctx, UpdatesRequest{}, WithCallRetryPolicy[UpdatesOption](retryOnce)) {
		require.NoError(t, err)
		break
	}
	require.EqualValues(t, 2, srv.watchCalls.Load())

	// A client-wide write policy retries only idempotent writes, silently.
	srv = &adoptionServer{writeErr: codes.Unavailable}
	c = newAdoptionClient(t, srv, WithOperationRetryPolicy[WriteOption](retryOnce))
	var touch, create rel.Txn
	require.NoError(t, touch.Touch(testTriple("document", "a", "viewer", "user", "alice", "")))
	require.NoError(t, create.Create(testTriple("document", "a", "viewer", "user", "alice", "")))
	_, err = c.Write(ctx, WriteRequest{Transaction: touch})
	require.NoError(t, err)
	require.EqualValues(t, 2, srv.writes.Load(), "the touch-only write was retried")
	srv.writes.Store(0)
	_, err = c.Write(ctx, WriteRequest{Transaction: create})
	require.ErrorIs(t, err, ErrUnavailable, "a create is not retried, and not rejected")
	require.EqualValues(t, 1, srv.writes.Load())

	// Never-idempotent operations reject a retrying operation policy at construction.
	_, err = NewFromConn(&grpc.ClientConn{}, WithOperationRetryPolicy[DeleteOption](retryOnce))
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = NewFromConn(&grpc.ClientConn{}, WithOperationRetryPolicy[ImportRelationshipsOption](RetryPolicy{}))
	require.NoError(t, err, "a zero-retry policy is allowed anywhere")

	// Generic helpers over OperationOption compile and work.
	srv = &adoptionServer{readSchemaErr: codes.Unavailable}
	_, err = newAdoptionClient(t, srv).ReadSchema(ctx, ReadSchemaRequest{}, noRetry[ReadSchemaOption]())
	require.Error(t, err)
	require.EqualValues(t, 1, srv.readSchema.Load())
}
