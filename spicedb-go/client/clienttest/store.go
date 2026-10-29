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
// not evaluate a schema. A permission check resolves, in order, to a result
// registered with SetCheckResult, then to the answer of the resolver set with
// SetCheckResolver (GrantAll grants everything), then to the stored
// relationships (a relationship on the resource whose relation is the
// permission's name and whose subject matches exactly or by wildcard grants
// it), and otherwise to NoPermission. LookupResources and LookupSubjects
// enumerate the objects of the requested type that appear in the stored
// relationships and return those check resolution grants. ReflectSchema
// returns the result registered with SetReflectSchemaResult.
//
// Options are honored where they change results: read, export and lookup
// cursors and limits, the export filter, the lookup permissionship filter,
// check context, and WithResponseMetadata targets (filled from
// SetResponseMetadata). Other options are accepted and ignored; inspect them
// with client.ResolveOptions if a test needs to.
//
// Every call is recorded (Calls), with its request and resolved options, so
// tests can assert on what was sent without a strict mock. Faults can be
// injected at defined points (SetFaultHook, FailNext): before any operation,
// before each import batch, before an import commits, and before each read,
// export, lookup or CheckIter item, so failure paths need no fake gRPC server.
package clienttest

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"sync"
	"time"

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
	resolver  CheckResolver
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
	OpLookupResources     = "lookup resources"
	OpLookupSubjects      = "lookup subjects"
	OpUpdates             = "watch"

	// Operations the Store records and fails as Unimplemented.
	OpExpandPermissionTree          = "expand permission tree"
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
	// StageItem runs before a read, export, lookup or CheckIter yields item
	// Fault.Item (0-based).
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
// matching relationships (in insertion order), or a LookupResources call after
// the first n granted resources; it is the cursor the Store puts on the n-th
// item. The format, the decimal count n, is stable for tests.
func CursorAfter(n int) string { return strconv.Itoa(n) }

// parseCursor reads a CursorAfter cursor; empty means the start.
func parseCursor(op, cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 {
		return 0, serverError(op, client.CodeInvalidArgument, "invalid cursor")
	}
	return n, nil
}

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

// SetCheckResult registers the result checks and lookups resolve to for the
// given resource, permission and subject. It takes precedence over the
// resolver and the stored relationships. A result whose permissionship is
// unspecified (and that carries no Err) is reported as NoPermission, since a
// real server never sends an unspecified permissionship.
func (s *Store) SetCheckResult(resource rel.ObjectRef, permission string, subject rel.SubjectRef, result client.CheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checks == nil {
		s.checks = map[string]client.CheckResult{}
	}
	s.checks[checkKey(resource, permission, subject)] = result
}

// CheckResolver answers a permission check that has no registered result. It
// returns the result and true to answer, or false to fall through to the
// stored relationships. The check's Context includes the call's context
// option. Return PermissionshipConditionalPermission (with MissingContext) to
// emulate a caveat the caller did not satisfy. An unspecified permissionship
// is reported as NoPermission.
type CheckResolver func(client.PermissionCheck) (client.CheckResult, bool)

// GrantAll is a CheckResolver that grants every check:
//
//	store.SetCheckResolver(clienttest.GrantAll)
func GrantAll(client.PermissionCheck) (client.CheckResult, bool) {
	return client.CheckResult{Permissionship: client.PermissionshipHasPermission}, true
}

// SetCheckResolver installs resolver, consulted for checks (including those a
// lookup makes) after the results registered with SetCheckResult and before
// the stored relationships, so tests over generated IDs need no per-ID
// registration. It runs with the Store locked and must not call the Store.
// Pass nil to remove it.
func (s *Store) SetCheckResolver(resolver CheckResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolver = resolver
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
	start, err := parseCursor(OpReadRelationships, cursor)
	if err != nil {
		return nil, nil, err
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

// Check resolves the check (see SetCheckResolver for the order), stamped with
// the store's current revision.
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
	return s.check(request, r.CheckContext), nil
}

// CheckBulk resolves each check like Check, returning results in input order.
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
		results[i] = s.check(req, r.CheckContext)
	}
	return results, nil
}

// check resolves one check in the documented order: a registered result,
// then the resolver, then the stored relationships, then NoPermission. The
// call's context option is merged into the check's context (the check's own
// keys win) before the resolver sees it. s.mu must be held.
func (s *Store) check(request client.PermissionCheck, callContext map[string]any) client.CheckResult {
	result, ok := s.checks[checkKey(request.Resource, request.Permission, request.Subject)]
	if !ok && s.resolver != nil {
		result, ok = s.resolver(withContext(request, callContext))
	}
	if !ok {
		result = s.fromRelationships(request)
	}
	// A real server always sets a permissionship and the real client fails
	// one it does not recognize, so the Store never produces an unspecified
	// one: a registered or resolved result left unspecified (such as a zero
	// CheckResult) denies, unless it carries an item error.
	if result.Err == nil && result.Permissionship != client.PermissionshipHasPermission &&
		result.Permissionship != client.PermissionshipConditionalPermission {
		result.Permissionship = client.PermissionshipNoPermission
	}
	result.CheckedAt = s.token()
	return result
}

func withContext(request client.PermissionCheck, callContext map[string]any) client.PermissionCheck {
	if len(callContext) == 0 {
		return request
	}
	merged := make(map[string]any, len(callContext)+len(request.Context))
	for k, v := range callContext {
		merged[k] = v
	}
	for k, v := range request.Context {
		merged[k] = v
	}
	request.Context = merged
	return request
}

// fromRelationships grants the permission when a stored, unexpired
// relationship on the resource has the permission's name as its relation and
// a subject that matches exactly or by wildcard. A caveated match grants
// conditionally (the Store does not evaluate caveats); an uncaveated match
// wins over a caveated one. Anything else is NoPermission. s.mu must be held.
func (s *Store) fromRelationships(request client.PermissionCheck) client.CheckResult {
	now := time.Now()
	result := client.CheckResult{Permissionship: client.PermissionshipNoPermission}
	for _, r := range s.rels {
		if r.Resource() != request.Resource || r.Relation() != request.Permission || !subjectMatches(r.Subject(), request.Subject) {
			continue
		}
		if exp := r.Expiration(); exp != nil && !exp.After(now) {
			continue
		}
		if r.CaveatName() == "" {
			return client.CheckResult{Permissionship: client.PermissionshipHasPermission}
		}
		result.Permissionship = client.PermissionshipConditionalPermission
	}
	return result
}

// subjectMatches reports whether a stored subject grants to the checked one:
// exactly, or as a wildcard (type:*) over a direct subject of that type.
func subjectMatches(stored, checked rel.SubjectRef) bool {
	if stored == checked {
		return true
	}
	return stored.Object().ID() == "*" && stored.Relation() == "" && checked.Relation() == "" &&
		stored.Object().Type() == checked.Object().Type()
}

// CheckIter resolves each check like Check, in input order.
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
			result := s.check(req, r.CheckContext)
			s.mu.Unlock()
			if !yield(result, nil) {
				return
			}
			i++
		}
	}
}

// objectIDs returns the IDs of objects of objectType that appear in stored
// relationships as resource or subject, in order of first appearance.
// s.mu must be held.
func (s *Store) objectIDs(objectType string) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(o rel.ObjectRef) {
		if o.Type() == objectType && !seen[o.ID()] {
			seen[o.ID()] = true
			ids = append(ids, o.ID())
		}
	}
	for _, r := range s.rels {
		add(r.Resource())
		add(r.Subject().Object())
	}
	return ids
}

func granted(result client.CheckResult) bool {
	return result.Permissionship == client.PermissionshipHasPermission ||
		result.Permissionship == client.PermissionshipConditionalPermission
}

func partialCaveat(result client.CheckResult) *client.PartialCaveatInfo {
	if result.Permissionship != client.PermissionshipConditionalPermission {
		return nil
	}
	return &client.PartialCaveatInfo{MissingRequiredContext: append([]string(nil), result.MissingContext...)}
}

// emit yields items, running the StageItem fault hook before each.
func emit[T any](s *Store, call Call, err error, items []T) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		if err != nil {
			yield(zero, err)
			return
		}
		for i, item := range items {
			if ferr := s.fault(call, StageItem, i); ferr != nil {
				yield(zero, ferr)
				return
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// LookupResources yields the objects of the requested type that appear in the
// stored relationships (as resource or subject) and that check resolution
// grants to the subject, in order of first appearance. Conditional results
// are yielded as conditional, with the resolved missing context, unless
// WithLookupResourcesPermissionshipFilter(LookupHasPermissionOnly) drops them.
// Each result's cursor is CursorAfter(n), where n counts granted results up
// to it (filtered ones included, as the server's cursor advances past them);
// limits count yielded results.
func (s *Store) LookupResources(_ context.Context, request client.LookupResourcesRequest, opts ...client.LookupResourcesOption) iter.Seq2[client.LookupResource, error] {
	r := client.ResolveOptions(opts...)
	call, err := s.begin(OpLookupResources, request, r)
	if err == nil {
		err = r.Err
	}
	if err == nil && (request.ResourceType == "" || request.Permission == "" || request.Subject.Validate() != nil) {
		err = invalid("resource type, permission and valid subject required")
	}
	start := 0
	if err == nil {
		start, err = parseCursor(OpLookupResources, r.Cursor)
	}
	var out []client.LookupResource
	if err == nil {
		s.mu.Lock()
		s.respond(r)
		at := s.token()
		pos := 0
		for _, id := range s.objectIDs(request.ResourceType) {
			if id == "*" {
				continue
			}
			result := s.check(client.PermissionCheck{Resource: rel.MustNewObject(request.ResourceType, id),
				Permission: request.Permission, Subject: request.Subject, Consistency: request.Consistency}, r.CheckContext)
			if !granted(result) {
				continue
			}
			pos++
			if pos <= start || (r.LookupPermissionshipFilter == client.LookupHasPermissionOnly && !result.HasPermission()) {
				continue
			}
			if r.Limit > 0 && uint64(len(out)) >= r.Limit {
				break
			}
			out = append(out, client.LookupResource{ResourceID: id, Permissionship: result.Permissionship,
				PartialCaveat: partialCaveat(result), LookedUpAt: at, AfterResultCursor: CursorAfter(pos)})
		}
		s.mu.Unlock()
	}
	return emit(s, call, err, out)
}

// LookupSubjects yields the objects of the requested subject type that appear
// in the stored relationships (as resource or subject) and that check
// resolution grants on the resource, in order of first appearance. A stored
// wildcard subject (type:*) appears as a "*" result, as SpiceDB reports it.
// Subjects carry the relation set by WithLookupSubjectsRelation (direct by
// default). Conditional results are yielded as conditional and the limit
// counts yielded results. Like the real client's, it has no cursor.
func (s *Store) LookupSubjects(_ context.Context, request client.LookupSubjectsRequest, opts ...client.LookupSubjectsOption) iter.Seq2[client.LookupSubject, error] {
	r := client.ResolveOptions(opts...)
	call, err := s.begin(OpLookupSubjects, request, r)
	if err == nil {
		err = r.Err
	}
	if err == nil && (request.Resource.Validate() != nil || request.Permission == "" || request.SubjectType == "") {
		err = invalid("valid resource, permission and subject type required")
	}
	var out []client.LookupSubject
	if err == nil {
		s.mu.Lock()
		s.respond(r)
		at := s.token()
		for _, id := range s.objectIDs(request.SubjectType) {
			subject, serr := rel.NewSubject(rel.MustNewObject(request.SubjectType, id), r.LookupSubjectsRelation)
			if serr != nil {
				continue
			}
			result := s.check(client.PermissionCheck{Resource: request.Resource, Permission: request.Permission,
				Subject: subject, Consistency: request.Consistency}, r.CheckContext)
			if !granted(result) {
				continue
			}
			if r.Limit > 0 && uint64(len(out)) >= r.Limit {
				break
			}
			out = append(out, client.LookupSubject{LookedUpAt: at, Subject: client.ResolvedSubject{
				SubjectID: id, Permissionship: result.Permissionship, PartialCaveat: partialCaveat(result)}})
		}
		s.mu.Unlock()
	}
	return emit(s, call, err, out)
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

// Ready is a no-op: the Store is always ready. It is not recorded.
func (s *Store) Ready(context.Context, ...client.ReadyOption) error { return nil }

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
