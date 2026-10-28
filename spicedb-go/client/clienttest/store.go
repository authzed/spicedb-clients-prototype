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
package clienttest

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"sync"

	"google.golang.org/grpc/metadata"

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
}

// NewStore returns a Store holding the given relationships.
func NewStore(initial ...rel.Relationship) *Store {
	s := &Store{}
	for _, r := range initial {
		s.upsert(r)
	}
	return s
}

// serverError builds the error the real client returns when SpiceDB answers
// operation op with code and message.
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
				err := serverError("write", client.CodeAlreadyExists, fmt.Sprintf("could not CREATE relationship `%s`, as it already existed", u.Relationship))
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
		if err != nil {
			yield(client.ReadRelationship{}, err)
			return
		}
		for i, rr := range matched {
			if !yield(client.ReadRelationship{Relationship: rr, AfterResultCursor: cursors[i], ReadAt: readAt}, nil) {
				return
			}
		}
	}
}

// ExportRelationships yields every relationship (or those matching the export
// filter option), honoring cursors and limits.
func (s *Store) ExportRelationships(_ context.Context, _ client.ExportRelationshipsRequest, opts ...client.ExportRelationshipsOption) iter.Seq2[client.ExportedRelationship, error] {
	r := client.ResolveOptions(opts...)
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
		if err != nil {
			yield(client.ExportedRelationship{}, err)
			return
		}
		for i, rr := range matched {
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
	if r.Err != nil {
		return client.ImportRelationshipsResult{}, r.Err
	}
	if request.Relationships == nil {
		return client.ImportRelationshipsResult{}, invalid("relationships iterator required")
	}
	var batch []rel.Relationship
	for rr := range request.Relationships {
		if err := rr.Validate(); err != nil {
			return client.ImportRelationshipsResult{}, invalid("relationship: %s", err)
		}
		batch = append(batch, rr)
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
		return client.DeleteResult{}, serverError("delete relationships", client.CodeInvalidArgument,
			fmt.Sprintf("found more than %d relationships to be deleted and partial deletion was not requested", request.Limit))
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
func (s *Store) ReadSchema(_ context.Context, _ client.ReadSchemaRequest, opts ...client.ReadSchemaOption) (client.ReadSchemaResult, error) {
	r := client.ResolveOptions(opts...)
	if r.Err != nil {
		return client.ReadSchemaResult{}, r.Err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	if !s.hasSchema {
		return client.ReadSchemaResult{}, serverError("read schema", client.CodeNotFound, "No schema has been defined; please call WriteSchema to start")
	}
	return client.ReadSchemaResult{Schema: s.schema, Revision: s.token()}, nil
}

// WriteSchema stores the schema text. It is not compiled or validated.
func (s *Store) WriteSchema(_ context.Context, request client.WriteSchemaRequest, opts ...client.WriteSchemaOption) (client.WriteSchemaResult, error) {
	r := client.ResolveOptions(opts...)
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
// stamped with the current revision; without one it fails with NOT_FOUND.
func (s *Store) ReflectSchema(_ context.Context, request client.ReflectSchemaRequest, opts ...client.ReflectSchemaOption) (client.ReflectSchemaResult, error) {
	r := client.ResolveOptions(opts...)
	if r.Err != nil {
		return client.ReflectSchemaResult{}, r.Err
	}
	if err := request.Consistency.Validate(); err != nil {
		return client.ReflectSchemaResult{}, invalid("consistency: %s", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond(r)
	if s.reflect == nil {
		return client.ReflectSchemaResult{}, serverError("reflect schema", client.CodeNotFound, "No schema has been defined; please call WriteSchema to start")
	}
	result := *s.reflect
	result.Revision = s.token()
	return result, nil
}

// Check returns the result registered with SetCheckResult, stamped with the
// store's current revision.
func (s *Store) Check(_ context.Context, request client.PermissionCheck, opts ...client.CheckOption) (client.CheckResult, error) {
	r := client.ResolveOptions(opts...)
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
