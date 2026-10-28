// Package clienttest provides an in-memory test double for the relationship
// and check operations of *client.Client.
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
// matching, preconditions, CREATE conflicts, atomic transactions). It does not
// evaluate a schema: permission checks return results registered with
// SetCheckResult. Options are accepted and ignored.
package clienttest

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"sync"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// Store is an in-memory relationship store. The zero value is ready to use and
// it is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	rels     []rel.Relationship // insertion order, for deterministic reads
	revision uint64
	checks   map[string]client.CheckResult
}

// NewStore returns a Store holding the given relationships.
func NewStore(initial ...rel.Relationship) *Store {
	s := &Store{}
	for _, r := range initial {
		s.upsert(r)
	}
	return s
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

// Write applies the transaction atomically with SpiceDB semantics:
// preconditions are evaluated first (FAILED_PRECONDITION on failure), CREATE of
// an existing relationship fails with ALREADY_EXISTS, and nothing is applied
// unless everything succeeds.
func (s *Store) Write(_ context.Context, request client.WriteRequest, _ ...client.WriteOption) (client.WriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.Transaction.Len() == 0 {
		return client.WriteResult{}, client.NewError(client.CodeInvalidArgument, "transaction requires updates")
	}
	for _, p := range request.Transaction.Preconditions() {
		matched := s.matchesAny(p.Filter)
		if (p.Operation == rel.PreconditionMustMatch && !matched) || (p.Operation == rel.PreconditionMustNotMatch && matched) {
			err := client.NewError(client.CodeFailedPrecondition, fmt.Sprintf("unable to satisfy write precondition `%s`", p))
			err.Reason = "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
			err.ReasonDomain = "authzed.com"
			return client.WriteResult{}, err
		}
	}
	staged := &Store{rels: append([]rel.Relationship(nil), s.rels...)}
	for _, u := range request.Transaction.Updates() {
		switch u.Operation {
		case rel.UpdateOperationCreate:
			if staged.index(u.Relationship) >= 0 {
				err := client.NewError(client.CodeAlreadyExists, fmt.Sprintf("could not CREATE relationship `%s`, as it already existed", u.Relationship))
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
			return client.WriteResult{}, client.NewError(client.CodeInvalidArgument, "unknown mutation operation")
		}
	}
	s.rels = staged.rels
	s.revision++
	return client.WriteResult{Revision: s.token()}, nil
}

// ReadRelationships yields a snapshot of the relationships matching the
// request's filter, in insertion order, each read at the store's current
// revision. Options, including a start cursor, are ignored.
func (s *Store) ReadRelationships(_ context.Context, request client.ReadRelationshipsRequest, _ ...client.ReadRelationshipsOption) iter.Seq2[client.ReadRelationship, error] {
	s.mu.Lock()
	readAt := s.token()
	var matched []rel.Relationship
	err := request.Filter.Validate()
	if err == nil {
		for _, r := range s.rels {
			if request.Filter.Matches(r) {
				matched = append(matched, r)
			}
		}
	}
	s.mu.Unlock()
	return func(yield func(client.ReadRelationship, error) bool) {
		if err != nil {
			yield(client.ReadRelationship{}, client.NewError(client.CodeInvalidArgument, err.Error()))
			return
		}
		for i, r := range matched {
			item := client.ReadRelationship{Relationship: r, AfterResultCursor: strconv.Itoa(i + 1), ReadAt: readAt}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// DeleteRelationships removes up to request.Limit relationships matching the
// filter, reporting Complete when no matching relationship remains.
func (s *Store) DeleteRelationships(_ context.Context, request client.DeleteRequest, _ ...client.DeleteOption) (client.DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := request.Filter.Validate(); err != nil {
		return client.DeleteResult{}, client.NewError(client.CodeInvalidArgument, err.Error())
	}
	if request.Limit == 0 {
		return client.DeleteResult{}, client.NewError(client.CodeInvalidArgument, "delete limit must be positive")
	}
	var kept []rel.Relationship
	var deleted uint64
	for _, r := range s.rels {
		if deleted < uint64(request.Limit) && request.Filter.Matches(r) {
			deleted++
			continue
		}
		kept = append(kept, r)
	}
	s.rels = kept
	s.revision++
	return client.DeleteResult{Revision: s.token(), DeletedCount: deleted, Complete: !s.matchesAny(request.Filter)}, nil
}

// Check returns the result registered with SetCheckResult, stamped with the
// store's current revision.
func (s *Store) Check(_ context.Context, request client.PermissionCheck, _ ...client.CheckOption) (client.CheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.check(request), nil
}

// CheckBulk returns the registered results in input order.
func (s *Store) CheckBulk(_ context.Context, requests []client.PermissionCheck, _ ...client.CheckOption) ([]client.CheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]client.CheckResult, len(requests))
	for i, r := range requests {
		results[i] = s.check(r)
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
