// Package clienttest provides an in-memory test double for *client.Client.
//
// Store's methods have the same signatures as the corresponding *client.Client
// methods, so a consumer interface such as
//
//	type authz interface {
//		Write(context.Context, client.WriteRequest, ...client.WriteOption) (client.WriteResult, error)
//		Check(context.Context, client.PermissionCheck, ...client.CheckOption) (client.CheckResult, error)
//	}
//
// is satisfied by both. Store applies SpiceDB's relationship semantics (filter
// matching, preconditions, CREATE conflicts, atomic transactions and imports)
// and returns errors with the same codes and message shape as the real client
// ("spicedb: <operation>: <server message>", with ServerMessage set). It does
// not evaluate a schema: permission checks return results registered with
// SetCheckResult, and ReflectSchema returns the result registered with
// SetReflectSchemaResult.
//
// Options are honored where they change results: read and export cursors and
// limits, the export filter, and WithResponseMetadata targets (filled from
// SetResponseMetadata). Other options are accepted and ignored; inspect them
// with client.ResolveOptions if a test needs to.
//
// Every call is recorded (Calls), with its request and resolved options, so
// tests can assert on what was sent without a strict mock. Faults can be
// injected at defined points (SetFaultHook, FailNext): before any operation,
// before each import batch, before an import commits, and before each read or
// export item, so failure paths need no fake gRPC server.
package clienttest

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"sync"

	"google.golang.org/grpc/metadata"

	proto "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// Store is an in-memory SpiceDB double. The zero value is ready to use and it
// is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	rels      []rel.Relationship // insertion order, for deterministic reads
	revision  uint64
	checks    map[string]client.CheckResult
	schema    string
	reflect   *client.ReflectSchemaResult
	header    metadata.MD
	trailer   metadata.MD
	hasSchema bool
	calls     []Call
	counts    map[string]int
	hook      func(Fault) error
	// omitSchemaRevision makes ReadSchema return no revision.
	omitSchemaRevision bool
}

var _ client.Interface = (*Store)(nil)

// NewStore returns a Store holding the given relationships.
func NewStore(initial ...rel.Relationship) *Store {
	s := &Store{}
	for _, r := range initial {
		s.upsert(r)
	}
	return s
}

// noSchema is SpiceDB's message for reading a schema before any was written.
const noSchema = "no schema has been defined; please call WriteSchema to start"

// ServerError builds the error the real client returns when SpiceDB answers
// operation op (an Op constant) with code and message, for fault hooks.
func ServerError(op string, code client.ErrorCode, message string) *client.Error {
	return serverError(op, code, message)
}

func serverError(op string, code client.ErrorCode, message string) *client.Error {
	err := client.NewError(code, message)
	err.Message = "spicedb: " + op + ": " + message
	return err
}

// invalid builds the error the real client returns for local validation.
func invalid(format string, args ...any) *client.Error {
	err := client.NewError(client.CodeInvalidArgument, "")
	err.Message = "spicedb: " + fmt.Sprintf(format, args...)
	err.ServerMessage = ""
	return err
}

func key(r rel.Relationship) string { return r.Filter().String() }

func checkKey(resource rel.ObjectRef, permission string, subject rel.SubjectRef) string {
	return resource.String() + "#" + permission + "@" + subject.String()
}

func (s *Store) token() consistency.ZedToken {
	return consistency.NewZedToken(strconv.FormatUint(s.revision, 10))
}

func (s *Store) index(r rel.Relationship) int {
	k := key(r)
	for i, existing := range s.rels {
		if key(existing) == k {
			return i
		}
	}
	return -1
}

func (s *Store) upsert(r rel.Relationship) {
	if i := s.index(r); i >= 0 {
		s.rels[i] = r
		return
	}
	s.rels = append(s.rels, r)
}

func (s *Store) matchesAny(f rel.Filter) bool {
	for _, r := range s.rels {
		if f.Matches(r) {
			return true
		}
	}
	return false
}

// respond fills any WithResponseMetadata targets with the metadata registered
// by SetResponseMetadata.
func (s *Store) respond(r client.ResolvedOptions) {
	header, trailer := r.ResponseMetadata()
	if header != nil {
		*header = s.header.Copy()
	}
	if trailer != nil {
		*trailer = s.trailer.Copy()
	}
}

// Operation names, as recorded in Call.Operation and matched by FailNext. They
// match the operation names in the real client's error messages.
const (
	OpWrite               = "write"
	OpReadRelationships   = "read relationships"
	OpExportRelationships = "export relationships"
	OpImportRelationships = "import relationships"
	OpDeleteRelationships = "delete relationships"
	OpReadSchema          = "read schema"
	OpWriteSchema         = "write schema"
	OpReflectSchema       = "reflect schema"
	OpCheck               = "check"
	OpCheckBulk           = "check bulk"
	OpCheckIter           = "check iter"
	OpDeleteAll           = "delete all relationships"
	OpUpdates             = "watch"

	// Operations the Store records and fails as Unimplemented.
	OpExpandPermissionTree          = "expand permission tree"
	OpLookupResources               = "lookup resources"
	OpLookupSubjects                = "lookup subjects"
	OpComputablePermissions         = "computable permissions"
	OpDependentRelations            = "dependent relations"
	OpDiffSchema                    = "diff schema"
	OpRegisterRelationshipCounter   = "register relationship counter"
	OpCountRelationships            = "count relationships"
	OpUnregisterRelationshipCounter = "unregister relationship counter"
	OpRoaringLookupResources        = "roaring lookup resources"
	OpCountRelationshipsByFilter    = "count relationships by filter"
	OpWatchPermissionSets           = "watch permission sets"
	OpLookupPermissionSets          = "lookup permission sets"
	OpDownloadPermissionSets        = "download permission sets"
)

// Call is one recorded operation.
type Call struct {
	// Operation is one of the Op constants.
	Operation string
	// Index counts calls of this operation, starting at 0.
	Index int
	// Request is the operation's request value (for CheckBulk, the slice of checks).
	Request any
	// Options are the call's resolved options.
	Options client.ResolvedOptions
}

// Stage identifies where in an operation a fault hook runs.
type Stage int

const (
	// StageCall runs before an operation does anything, including before a
	// stream yields its first item.
	StageCall Stage = iota
	// StageImportBatch runs before import batch Fault.Item (0-based) is sent.
	// Batches have the call's batch size (WithImportRelationshipsBatchSize,
	// default 1000).
	StageImportBatch
	// StageImportCommit runs after every batch, before the import commits.
	StageImportCommit
	// StageItem runs before a read or export yields item Fault.Item (0-based).
	StageItem
)

// Fault is the point at which a fault hook is consulted.
type Fault struct {
	Call  Call
	Stage Stage
	// Item is the batch index for StageImportBatch and the item index for
	// StageItem; 0 otherwise.
	Item int
	// Batch holds the relationships in the batch at StageImportBatch.
	Batch []rel.Relationship
}

// SchemaRevision selects whether ReadSchema reports a revision.
type SchemaRevision uint8

const (
	// RevisionIncluded reports the store's revision. This is the default.
	RevisionIncluded SchemaRevision = iota
	// RevisionOmitted reports none, as some hosted SpiceDB deployments do,
	// so code that falls back to another revision source can be tested.
	RevisionOmitted
)

// SetReadSchemaRevision selects whether ReadSchema reports a revision.
func (s *Store) SetReadSchemaRevision(mode SchemaRevision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.omitSchemaRevision = mode == RevisionOmitted
}

// CursorAfter returns a cursor that resumes a read or export after the first n
// matching relationships (in insertion order), the same cursor the Store puts
// on the n-th item. The format, the decimal count n, is stable for tests.
func CursorAfter(n int) string { return strconv.Itoa(n) }

// SetFaultHook installs hook, which runs at every Fault point; a non-nil
// error is returned (or yielded) by the operation at that point, and a failed
// write or import applies nothing. Build errors with ServerError to match the
// real client's shape. The hook must not call the Store. Pass nil to remove it.
func (s *Store) SetFaultHook(hook func(Fault) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hook = hook
}

// FailNext makes the next call of operation fail with err before doing
// anything; later calls succeed. It replaces any fault hook.
func (s *Store) FailNext(operation string, err error) {
	failed := false
	s.SetFaultHook(func(f Fault) error {
		if !failed && f.Stage == StageCall && f.Call.Operation == operation {
			failed = true
			return err
		}
		return nil
	})
}

// Calls returns every recorded call, in order.
func (s *Store) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// CallsOf returns the recorded calls of one operation, in order.
func (s *Store) CallsOf(operation string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Operation == operation {
			out = append(out, c)
		}
	}
	return out
}

// begin records a call and runs the StageCall fault hook.
func (s *Store) begin(operation string, request any, r client.ResolvedOptions) (Call, error) {
	s.mu.Lock()
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	call := Call{Operation: operation, Index: s.counts[operation], Request: request, Options: r}
	s.counts[operation]++
	s.calls = append(s.calls, call)
	s.mu.Unlock()
	return call, s.fault(call, StageCall, 0)
}

// fault runs the hook, if any, at one point of a call.
func (s *Store) fault(call Call, stage Stage, item int) error {
	return s.faultBatch(call, stage, item, nil)
}

func (s *Store) faultBatch(call Call, stage Stage, item int, batch []rel.Relationship) error {
	s.mu.Lock()
	hook := s.hook
	s.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(Fault{Call: call, Stage: stage, Item: item, Batch: append([]rel.Relationship(nil), batch...)})
}

// Relationships returns a snapshot of every stored relationship.
func (s *Store) Relationships() []rel.Relationship {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rel.Relationship(nil), s.rels...)
}

// SetCheckResult registers the result Check and CheckBulk return for the
// given resource, permission and subject. Unregistered checks return
// PermissionshipNoPermission.
func (s *Store) SetCheckResult(resource rel.ObjectRef, permission string, subject rel.SubjectRef, result client.CheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checks == nil {
		s.checks = map[string]client.CheckResult{}
	}
	s.checks[checkKey(resource, permission, subject)] = result
}

// SetReflectSchemaResult registers the result ReflectSchema returns.
func (s *Store) SetReflectSchemaResult(result client.ReflectSchemaResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reflect = &result
}

// SetResponseMetadata sets the headers and trailers every operation reports to
// WithResponseMetadata targets.
func (s *Store) SetResponseMetadata(header, trailer metadata.MD) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.header, s.trailer = header.Copy(), trailer.Copy()
}

// Write applies the transaction atomically with SpiceDB semantics:
// preconditions are evaluated first (FAILED_PRECONDITION on failure), CREATE of
// an existing relationship fails with ALREADY_EXISTS, and nothing is applied
// unless everything succeeds. An empty transaction checks its preconditions
// and returns the current revision, as SpiceDB does.
func (s *Store) Write(_ context.Context, request client.WriteRequest, opts ...client.WriteOption) (client.WriteResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpWrite, request, r); ferr != nil {
		return client.WriteResult{}, ferr
	}
	if r.Err != nil {
		return client.WriteResult{}, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	for _, p := range request.Transaction.Preconditions() {
		matched := s.matchesAny(p.Filter)
		if (p.Operation == rel.PreconditionMustMatch && !matched) || (p.Operation == rel.PreconditionMustNotMatch && matched) {
			err := serverError("write", client.CodeFailedPrecondition, fmt.Sprintf("unable to satisfy write precondition `%s`", p))
			err.Reason = "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
			err.ReasonDomain = "authzed.com"
			return client.WriteResult{}, err
		}
	}
	if request.Transaction.Len() == 0 {
		return client.WriteResult{Revision: s.token()}, nil
	}
	staged := &Store{rels: append([]rel.Relationship(nil), s.rels...)}
	for _, u := range request.Transaction.Updates() {
		switch u.Operation {
		case rel.UpdateOperationCreate:
			if staged.index(u.Relationship) >= 0 {
				err := serverError("write", client.CodeAlreadyExists, fmt.Sprintf("could not CREATE relationship `%s`, as it already existed. If this is persistent, please switch to TOUCH operations or specify a precondition", u.Relationship))
				err.Reason = "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP"
				err.ReasonDomain = "authzed.com"
				return client.WriteResult{}, err
			}
			staged.upsert(u.Relationship)
		case rel.UpdateOperationTouch:
			staged.upsert(u.Relationship)
		case rel.UpdateOperationDelete:
			if i := staged.index(u.Relationship); i >= 0 {
				staged.rels = append(staged.rels[:i], staged.rels[i+1:]...)
			}
		default:
			return client.WriteResult{}, invalid("unknown mutation operation")
		}
	}
	s.rels = staged.rels
	s.revision++
	return client.WriteResult{Revision: s.token()}, nil
}

// page returns the matching relationships after cursor, up to limit (0 means
// all), each with its cursor: the 1-based position it occupies in matching order.
func (s *Store) page(f func(rel.Relationship) bool, cursor string, limit uint64) ([]rel.Relationship, []string, error) {
	start := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return nil, nil, serverError("read relationships", client.CodeInvalidArgument, "invalid cursor")
		}
		start = n
	}
	var out []rel.Relationship
	var cursors []string
	pos := 0
	for _, r := range s.rels {
		if !f(r) {
			continue
		}
		pos++
		if pos <= start {
			continue
		}
		if limit > 0 && uint64(len(out)) >= limit {
			break
		}
		out = append(out, r)
		cursors = append(cursors, strconv.Itoa(pos))
	}
	return out, cursors, nil
}

// ReadRelationships yields a snapshot of the relationships matching the
// request's filter, in insertion order, each read at the store's current
// revision. Cursors and limits are honored.
func (s *Store) ReadRelationships(_ context.Context, request client.ReadRelationshipsRequest, opts ...client.ReadRelationshipsOption) iter.Seq2[client.ReadRelationship, error] {
	r := client.ResolveOptions(opts...)
	call, callErr := s.begin(OpReadRelationships, request, r)
	s.mu.Lock()
	readAt := s.token()
	err := r.Err
	if err == nil {
		if verr := request.Filter.Validate(); verr != nil {
			err = invalid("filter: %s", verr)
		}
	}
	var matched []rel.Relationship
	var cursors []string
	if err == nil {
		matched, cursors, err = s.page(request.Filter.Matches, r.Cursor, r.Limit)
		s.respond(r)
	}
	s.mu.Unlock()
	return func(yield func(client.ReadRelationship, error) bool) {
		if callErr != nil {
			err = callErr
		}
		if err != nil {
			yield(client.ReadRelationship{}, err)
			return
		}
		for i, rr := range matched {
			if ferr := s.fault(call, StageItem, i); ferr != nil {
				yield(client.ReadRelationship{}, ferr)
				return
			}
			if !yield(client.ReadRelationship{Relationship: rr, AfterResultCursor: cursors[i], ReadAt: readAt}, nil) {
				return
			}
		}
	}
}

// ExportRelationships yields every relationship (or those matching the export
// filter option), honoring cursors and limits.
func (s *Store) ExportRelationships(_ context.Context, request client.ExportRelationshipsRequest, opts ...client.ExportRelationshipsOption) iter.Seq2[client.ExportedRelationship, error] {
	r := client.ResolveOptions(opts...)
	call, callErr := s.begin(OpExportRelationships, request, r)
	s.mu.Lock()
	match := func(rel.Relationship) bool { return true }
	if r.ExportFilter != nil {
		match = r.ExportFilter.Matches
	}
	err := r.Err
	var matched []rel.Relationship
	var cursors []string
	if err == nil {
		matched, cursors, err = s.page(match, r.Cursor, r.Limit)
		s.respond(r)
	}
	s.mu.Unlock()
	return func(yield func(client.ExportedRelationship, error) bool) {
		if callErr != nil {
			err = callErr
		}
		if err != nil {
			yield(client.ExportedRelationship{}, err)
			return
		}
		for i, rr := range matched {
			if ferr := s.fault(call, StageItem, i); ferr != nil {
				yield(client.ExportedRelationship{}, ferr)
				return
			}
			if !yield(client.ExportedRelationship{Relationship: rr, AfterResultCursor: cursors[i]}, nil) {
				return
			}
		}
	}
}

// ImportRelationships adds every relationship in one transaction, as SpiceDB
// does: if any already exists (or repeats within the import), nothing is
// added and the error is ALREADY_EXISTS.
func (s *Store) ImportRelationships(_ context.Context, request client.ImportRelationshipsRequest, opts ...client.ImportRelationshipsOption) (client.ImportRelationshipsResult, error) {
	r := client.ResolveOptions(opts...)
	call, ferr := s.begin(OpImportRelationships, request, r)
	if ferr != nil {
		return client.ImportRelationshipsResult{}, ferr
	}
	if r.Err != nil {
		return client.ImportRelationshipsResult{}, r.Err
	}
	if request.Relationships == nil {
		return client.ImportRelationshipsResult{}, invalid("relationships iterator required")
	}
	var batch []rel.Relationship
	batchSize := int(max(r.PageSize, 1))
	for rr := range request.Relationships {
		if err := rr.Validate(); err != nil {
			return client.ImportRelationshipsResult{}, invalid("relationship: %s", err)
		}
		batch = append(batch, rr)
	}
	for start := 0; start < len(batch); start += batchSize {
		end := min(start+batchSize, len(batch))
		if err := s.faultBatch(call, StageImportBatch, start/batchSize, batch[start:end]); err != nil {
			return client.ImportRelationshipsResult{}, err
		}
	}
	if err := s.fault(call, StageImportCommit, 0); err != nil {
		return client.ImportRelationshipsResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	staged := &Store{rels: append([]rel.Relationship(nil), s.rels...)}
	for _, rr := range batch {
		if staged.index(rr) >= 0 {
			return client.ImportRelationshipsResult{}, serverError("import relationships", client.CodeAlreadyExists,
				fmt.Sprintf("found duplicate relationship `%s` while importing", rr))
		}
		staged.rels = append(staged.rels, rr)
	}
	s.rels = staged.rels
	s.revision++
	return client.ImportRelationshipsResult{ImportedCount: uint64(len(batch))}, nil
}

// DeleteRelationships removes up to request.Limit relationships matching the
// filter, reporting Complete when no matching relationship remains. With
// WithDeletePartial(PartialDisallowed) it deletes all or nothing: more matches
// than a positive limit fail with INVALID_ARGUMENT, as SpiceDB does.
func (s *Store) DeleteRelationships(_ context.Context, request client.DeleteRequest, opts ...client.DeleteOption) (client.DeleteResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpDeleteRelationships, request, r); ferr != nil {
		return client.DeleteResult{}, ferr
	}
	if r.Err != nil {
		return client.DeleteResult{}, r.Err
	}
	if err := request.Filter.Validate(); err != nil {
		return client.DeleteResult{}, invalid("filter: %s", err)
	}
	allOrNothing := r.PartialDeletion == client.PartialDisallowed
	if request.Limit == 0 && !allOrNothing {
		return client.DeleteResult{}, invalid("delete total limit must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	for _, p := range r.Preconditions {
		matched := s.matchesAny(p.Filter)
		if (p.Operation == rel.PreconditionMustMatch && !matched) || (p.Operation == rel.PreconditionMustNotMatch && matched) {
			return client.DeleteResult{}, serverError("delete relationships", client.CodeFailedPrecondition,
				fmt.Sprintf("unable to satisfy write precondition `%s`", p))
		}
	}
	matches := 0
	for _, rr := range s.rels {
		if request.Filter.Matches(rr) {
			matches++
		}
	}
	if allOrNothing && request.Limit > 0 && matches > int(request.Limit) {
		err := serverError("delete relationships", client.CodeInvalidArgument,
			fmt.Sprintf("found more than %d relationships to be deleted and partial deletion was not requested", request.Limit))
		err.Reason = "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE"
		err.ReasonDomain = "authzed.com"
		return client.DeleteResult{}, err
	}
	var kept []rel.Relationship
	var deleted uint64
	for _, rr := range s.rels {
		if (allOrNothing || deleted < uint64(request.Limit)) && request.Filter.Matches(rr) {
			deleted++
			continue
		}
		kept = append(kept, rr)
	}
	s.rels = kept
	s.revision++
	return client.DeleteResult{Revision: s.token(), DeletedCount: deleted, Complete: !s.matchesAny(request.Filter)}, nil
}

// ReadSchema returns the schema last written, or NOT_FOUND, as SpiceDB does.
func (s *Store) ReadSchema(_ context.Context, request client.ReadSchemaRequest, opts ...client.ReadSchemaOption) (client.ReadSchemaResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpReadSchema, request, r); ferr != nil {
		return client.ReadSchemaResult{}, ferr
	}
	if r.Err != nil {
		return client.ReadSchemaResult{}, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	if !s.hasSchema {
		return client.ReadSchemaResult{}, serverError("read schema", client.CodeNotFound, noSchema)
	}
	result := client.ReadSchemaResult{Schema: s.schema, Revision: s.token()}
	if s.omitSchemaRevision {
		result.Revision = consistency.ZedToken{}
	}
	return result, nil
}

// WriteSchema stores the schema text. It is not compiled or validated.
func (s *Store) WriteSchema(_ context.Context, request client.WriteSchemaRequest, opts ...client.WriteSchemaOption) (client.WriteSchemaResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpWriteSchema, request, r); ferr != nil {
		return client.WriteSchemaResult{}, ferr
	}
	if r.Err != nil {
		return client.WriteSchemaResult{}, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	s.schema, s.hasSchema = request.Schema, true
	s.revision++
	return client.WriteSchemaResult{Revision: s.token()}, nil
}

// ReflectSchema returns the result registered with SetReflectSchemaResult,
// stamped with the current revision. Without one it returns an empty
// result, as SpiceDB does for an empty schema.
func (s *Store) ReflectSchema(_ context.Context, request client.ReflectSchemaRequest, opts ...client.ReflectSchemaOption) (client.ReflectSchemaResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpReflectSchema, request, r); ferr != nil {
		return client.ReflectSchemaResult{}, ferr
	}
	if r.Err != nil {
		return client.ReflectSchemaResult{}, r.Err
	}
	if err := request.Consistency.Validate(); err != nil {
		return client.ReflectSchemaResult{}, invalid("consistency: %s", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	var result client.ReflectSchemaResult
	if s.reflect != nil {
		result = *s.reflect
	}
	result.Revision = s.token()
	return result, nil
}

// Check returns the result registered with SetCheckResult, stamped with the
// store's current revision.
func (s *Store) Check(_ context.Context, request client.PermissionCheck, opts ...client.CheckOption) (client.CheckResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpCheck, request, r); ferr != nil {
		return client.CheckResult{}, ferr
	}
	if r.Err != nil {
		return client.CheckResult{}, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	return s.check(request), nil
}

// CheckBulk returns the registered results in input order.
func (s *Store) CheckBulk(_ context.Context, requests []client.PermissionCheck, opts ...client.CheckOption) ([]client.CheckResult, error) {
	r := client.ResolveOptions(opts...)
	if _, ferr := s.begin(OpCheckBulk, requests, r); ferr != nil {
		return nil, ferr
	}
	if r.Err != nil {
		return nil, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	results := make([]client.CheckResult, len(requests))
	for i, req := range requests {
		results[i] = s.check(req)
	}
	return results, nil
}

func (s *Store) check(request client.PermissionCheck) client.CheckResult {
	result, ok := s.checks[checkKey(request.Resource, request.Permission, request.Subject)]
	if !ok {
		result = client.CheckResult{Permissionship: client.PermissionshipNoPermission}
	}
	result.CheckedAt = s.token()
	return result
}

// CheckIter checks each request with the registered results, in input order.
func (s *Store) CheckIter(_ context.Context, requests iter.Seq[client.PermissionCheck], opts ...client.CheckOption) iter.Seq2[client.CheckResult, error] {
	r := client.ResolveOptions(opts...)
	call, callErr := s.begin(OpCheckIter, requests, r)
	return func(yield func(client.CheckResult, error) bool) {
		if callErr != nil || r.Err != nil {
			yield(client.CheckResult{}, cmpErr(callErr, r.Err))
			return
		}
		i := 0
		for req := range requests {
			if err := s.fault(call, StageItem, i); err != nil {
				yield(client.CheckResult{}, err)
				return
			}
			s.mu.Lock()
			result := s.check(req)
			s.mu.Unlock()
			if !yield(result, nil) {
				return
			}
			i++
		}
	}
}

// DeleteAllRelationshipsNonAtomic deletes every relationship matching the
// filter in one step, checking preconditions first, and reports progress once.
func (s *Store) DeleteAllRelationshipsNonAtomic(_ context.Context, request client.DeleteAllRequest, opts ...client.DeleteAllOption) (client.DeleteResult, error) {
	r := client.ResolveOptions(opts...)
	if r.Err != nil {
		return client.DeleteResult{}, r.Err
	}
	if _, ferr := s.begin(OpDeleteAll, request, r); ferr != nil {
		return client.DeleteResult{}, ferr
	}
	if err := request.Filter.Validate(); err != nil {
		return client.DeleteResult{}, invalid("filter: %s", err)
	}
	s.mu.Lock()
	for _, p := range r.Preconditions {
		matched := s.matchesAny(p.Filter)
		if (p.Operation == rel.PreconditionMustMatch && !matched) || (p.Operation == rel.PreconditionMustNotMatch && matched) {
			s.mu.Unlock()
			return client.DeleteResult{}, serverError(OpDeleteRelationships, client.CodeFailedPrecondition,
				fmt.Sprintf("unable to satisfy write precondition `%s`", p))
		}
	}
	var kept []rel.Relationship
	var deleted uint64
	for _, rr := range s.rels {
		if request.Filter.Matches(rr) {
			deleted++
			continue
		}
		kept = append(kept, rr)
	}
	s.rels = kept
	s.revision++
	result := client.DeleteResult{Revision: s.token(), DeletedCount: deleted, Complete: true}
	s.mu.Unlock()
	if r.DeleteProgress != nil {
		r.DeleteProgress(result)
	}
	return result, nil
}

// Close does nothing; the Store holds no transport.
func (s *Store) Close() error { return nil }

// RawProto returns nil; the Store has no protocol client.
func (s *Store) RawProto() *proto.Client { return nil }

func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// unimplemented records the call, consults the fault hook, and otherwise
// fails with Unimplemented in the real client's error shape.
func unimplemented[T any](s *Store, op string, request any, r client.ResolvedOptions) (T, error) {
	var zero T
	if _, err := s.begin(op, request, r); err != nil {
		return zero, err
	}
	if r.Err != nil {
		return zero, r.Err
	}
	return zero, serverError(op, client.CodeUnimplemented, "not implemented by clienttest.Store")
}

func unimplementedStream[T any](s *Store, op string, request any, r client.ResolvedOptions) iter.Seq2[T, error] {
	_, err := unimplemented[T](s, op, request, r)
	return func(yield func(T, error) bool) {
		var zero T
		yield(zero, err)
	}
}

// The remaining operations have no in-memory semantics. They are recorded,
// consult the fault hook, and fail with Unimplemented.

func (s *Store) ExpandPermissionTree(_ context.Context, request client.ExpandPermissionTreeRequest, opts ...client.ExpandPermissionTreeOption) (client.ExpandPermissionTreeResult, error) {
	return unimplemented[client.ExpandPermissionTreeResult](s, OpExpandPermissionTree, request, client.ResolveOptions(opts...))
}

func (s *Store) LookupResources(_ context.Context, request client.LookupResourcesRequest, opts ...client.LookupResourcesOption) iter.Seq2[client.LookupResource, error] {
	return unimplementedStream[client.LookupResource](s, OpLookupResources, request, client.ResolveOptions(opts...))
}

func (s *Store) LookupSubjects(_ context.Context, request client.LookupSubjectsRequest, opts ...client.LookupSubjectsOption) iter.Seq2[client.LookupSubject, error] {
	return unimplementedStream[client.LookupSubject](s, OpLookupSubjects, request, client.ResolveOptions(opts...))
}

func (s *Store) ComputablePermissions(_ context.Context, request client.ComputablePermissionsRequest, opts ...client.ComputablePermissionsOption) (client.ComputablePermissionsResult, error) {
	return unimplemented[client.ComputablePermissionsResult](s, OpComputablePermissions, request, client.ResolveOptions(opts...))
}

func (s *Store) DependentRelations(_ context.Context, request client.DependentRelationsRequest, opts ...client.DependentRelationsOption) (client.DependentRelationsResult, error) {
	return unimplemented[client.DependentRelationsResult](s, OpDependentRelations, request, client.ResolveOptions(opts...))
}

func (s *Store) DiffSchema(_ context.Context, request client.DiffSchemaRequest, opts ...client.DiffSchemaOption) (client.DiffSchemaResult, error) {
	return unimplemented[client.DiffSchemaResult](s, OpDiffSchema, request, client.ResolveOptions(opts...))
}

func (s *Store) RegisterRelationshipCounter(_ context.Context, request client.RegisterRelationshipCounterRequest, opts ...client.RegisterRelationshipCounterOption) (client.RegisterRelationshipCounterResult, error) {
	return unimplemented[client.RegisterRelationshipCounterResult](s, OpRegisterRelationshipCounter, request, client.ResolveOptions(opts...))
}

func (s *Store) CountRelationships(_ context.Context, request client.CountRelationshipsRequest, opts ...client.CountRelationshipsOption) (client.CountRelationshipsResult, error) {
	return unimplemented[client.CountRelationshipsResult](s, OpCountRelationships, request, client.ResolveOptions(opts...))
}

func (s *Store) UnregisterRelationshipCounter(_ context.Context, request client.UnregisterRelationshipCounterRequest, opts ...client.UnregisterRelationshipCounterOption) (client.UnregisterRelationshipCounterResult, error) {
	return unimplemented[client.UnregisterRelationshipCounterResult](s, OpUnregisterRelationshipCounter, request, client.ResolveOptions(opts...))
}

func (s *Store) RoaringLookupResources(_ context.Context, request client.RoaringLookupResourcesRequest, opts ...client.RoaringLookupResourcesOption) (client.RoaringLookupResourcesResult, error) {
	return unimplemented[client.RoaringLookupResourcesResult](s, OpRoaringLookupResources, request, client.ResolveOptions(opts...))
}

func (s *Store) CountRelationshipsByFilter(_ context.Context, request client.CountRelationshipsByFilterRequest, opts ...client.CountRelationshipsByFilterOption) (client.CountRelationshipsByFilterResult, error) {
	return unimplemented[client.CountRelationshipsByFilterResult](s, OpCountRelationshipsByFilter, request, client.ResolveOptions(opts...))
}

func (s *Store) WatchPermissionSets(_ context.Context, request client.WatchPermissionSetsRequest, opts ...client.WatchPermissionSetsOption) iter.Seq2[client.PermissionSetEvent, error] {
	return unimplementedStream[client.PermissionSetEvent](s, OpWatchPermissionSets, request, client.ResolveOptions(opts...))
}

func (s *Store) LookupPermissionSets(_ context.Context, request client.LookupPermissionSetsRequest, opts ...client.LookupPermissionSetsOption) iter.Seq2[client.LookupPermissionSet, error] {
	return unimplementedStream[client.LookupPermissionSet](s, OpLookupPermissionSets, request, client.ResolveOptions(opts...))
}

func (s *Store) DownloadPermissionSets(_ context.Context, request client.DownloadPermissionSetsRequest, opts ...client.DownloadPermissionSetsOption) (client.DownloadPermissionSetsResult, error) {
	return unimplemented[client.DownloadPermissionSetsResult](s, OpDownloadPermissionSets, request, client.ResolveOptions(opts...))
}

func (s *Store) Updates(_ context.Context, request client.UpdatesRequest, opts ...client.UpdatesOption) iter.Seq2[client.WatchEvent, error] {
	return unimplementedStream[client.WatchEvent](s, OpUpdates, request, client.ResolveOptions(opts...))
}
