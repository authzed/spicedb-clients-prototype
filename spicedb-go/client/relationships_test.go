package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// deleteStubServer is a stub PermissionsService whose DeleteRelationships
// implementation records every request it receives and replays canned
// responses in order, so tests can assert on exactly what the client sends
// (preconditions, limit) and on multi-call auto-paging behavior.
type deleteStubServer struct {
	v1.UnimplementedPermissionsServiceServer

	// responses are replayed in order, one per call; if there are more calls
	// than responses, the last response is reused.
	responses []*v1.DeleteRelationshipsResponse

	requests []*v1.DeleteRelationshipsRequest
	failAt   int
	failure  error
	cancel   context.CancelFunc
}

func (s *deleteStubServer) DeleteRelationships(_ context.Context, req *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	s.requests = append(s.requests, req)
	if s.failAt == len(s.requests) {
		return nil, s.failure
	}
	if s.cancel != nil && len(s.requests) == 1 {
		s.cancel()
	}

	if len(s.responses) == 0 {
		return &v1.DeleteRelationshipsResponse{
			DeletedAt:        &v1.ZedToken{Token: "rev-1"},
			DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE,
		}, nil
	}

	idx := len(s.requests) - 1
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	return s.responses[idx], nil
}

// startDeleteStubServer starts an in-process gRPC server backed by bufconn,
// exposing a PermissionsService backed by the given stub, and returns a
// dialer for it.
func startDeleteStubServer(t *testing.T, stub *deleteStubServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, stub)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

func TestDeleteBoundIsSingleRPCAndPreservesCountAndCursor(t *testing.T) {
	stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{{DeletedAt: &v1.ZedToken{Token: "rev-1"}, RelationshipsDeletedCount: 7, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, AfterResultCursor: &v1.Cursor{Token: "resume"}}}}
	c := newTestClient(t, startDeleteStubServer(t, stub))
	f := testFilter("document").WithResourceID("a")
	result, e := c.DeleteRelationships(context.Background(), DeleteRequest{Filter: f, Limit: 7}, WithDeleteMustMatch(f), WithDeleteMustNotMatch(f), WithDeleteCursor("start"))
	require.NoError(t, e)
	require.Equal(t, DeleteResult{Revision: consistency.NewZedToken("rev-1"), DeletedCount: 7, Complete: false, Cursor: "resume"}, result)
	require.Len(t, stub.requests, 1)
	r := stub.requests[0]
	require.Equal(t, uint32(7), r.OptionalLimit)
	require.True(t, r.OptionalAllowPartialDeletions)
	require.Equal(t, "start", r.OptionalCursor.Token)
	require.Len(t, r.OptionalPreconditions, 2)
	require.Equal(t, v1.Precondition_OPERATION_MUST_MATCH, r.OptionalPreconditions[0].Operation)
	require.Equal(t, v1.Precondition_OPERATION_MUST_NOT_MATCH, r.OptionalPreconditions[1].Operation)
	require.Equal(t, "a", r.RelationshipFilter.OptionalResourceId)
}
func TestDeleteRejectsInvalidLimitsAndFiltersBeforeRPC(t *testing.T) {
	c := &Client{}
	f := testFilter("document")
	bad := f.WithSubjectID("alice")
	_, e := c.DeleteRelationships(context.Background(), DeleteRequest{Filter: f})
	require.ErrorIs(t, e, ErrInvalidArgument)
	for _, filter := range []rel.Filter{bad, f.WithSubjectRelation(rel.DirectRelation()), f.WithResourceID("a").WithResourceIDPrefix("prefix")} {
		_, e = c.DeleteRelationships(context.Background(), DeleteRequest{Filter: filter, Limit: 1})
		require.ErrorIs(t, e, ErrInvalidArgument)
	}
	_, e = c.DeleteRelationships(context.Background(), DeleteRequest{Filter: f, Limit: 1}, WithDeleteMustMatch(bad))
	require.ErrorIs(t, e, ErrInvalidArgument)
	_, e = c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: f}, WithDeleteAllPageSize(0))
	require.ErrorIs(t, e, ErrInvalidArgument)
}
func TestDeleteAllFollowsCursorReevaluatesPreconditionsAndSumsAcknowledgedPages(t *testing.T) {
	for _, cursorMode := range []string{"present", "absent", "empty"} {
		withCursor := cursorMode == "present"
		t.Run(fmt.Sprint(cursorMode), func(t *testing.T) {
			partial := &v1.DeleteRelationshipsResponse{DeletedAt: &v1.ZedToken{Token: "rev-1"}, RelationshipsDeletedCount: 3, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL}
			if withCursor {
				partial.AfterResultCursor = &v1.Cursor{Token: "resume"}
			} else if cursorMode == "empty" {
				partial.AfterResultCursor = &v1.Cursor{}
			}
			stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{partial, {DeletedAt: &v1.ZedToken{Token: "rev-2"}, RelationshipsDeletedCount: 2, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE}}}
			c := newTestClient(t, startDeleteStubServer(t, stub))
			f := testFilter("document")
			r, e := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: f}, WithDeleteAllPageSize(3), WithDeleteAllMustMatch(f), WithDeleteAllMustNotMatch(f))
			require.NoError(t, e)
			require.Equal(t, uint64(5), r.DeletedCount)
			require.EqualValues(t, "rev-2", r.Revision.String())
			require.True(t, r.Complete)
			require.Len(t, stub.requests, 2)
			for _, req := range stub.requests {
				require.Equal(t, uint32(3), req.OptionalLimit)
				require.Len(t, req.OptionalPreconditions, 2)
			}
			if withCursor {
				require.Equal(t, "resume", stub.requests[1].OptionalCursor.GetToken())
			} else {
				require.Nil(t, stub.requests[1].OptionalCursor)
			}
		})
	}
}
func TestDeleteAllDefaultPageSizeAndNonprogress(t *testing.T) {
	stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{{RelationshipsDeletedCount: 0, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL}}}
	c := newTestClient(t, startDeleteStubServer(t, stub))
	_, e := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: testFilter("document")})
	require.Error(t, e)
	var progress *DeleteProgressError
	require.ErrorAs(t, e, &progress)
	require.Contains(t, e.Error(), "no progress")
	require.Len(t, stub.requests, 1)
	require.Equal(t, uint32(1000), stub.requests[0].OptionalLimit)
}
func TestDeleteAllInterruptedProgressPreservesCauseAndNeverReplays(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			source := status.Error(code, "ambiguous second page")
			stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{{DeletedAt: &v1.ZedToken{Token: "confirmed"}, RelationshipsDeletedCount: 4, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, AfterResultCursor: &v1.Cursor{Token: "resume"}}}, failAt: 2, failure: source}
			c := newTestClient(t, startDeleteStubServer(t, stub))
			r, e := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: testFilter("document")})
			require.Error(t, e)
			require.Equal(t, uint64(4), r.DeletedCount)
			require.EqualValues(t, "confirmed", r.Revision.String())
			require.Equal(t, "resume", r.Cursor)
			var progress *DeleteProgressError
			require.ErrorAs(t, e, &progress)
			require.Equal(t, r, progress.Progress)
			var native *Error
			require.ErrorAs(t, e, &native)
			require.Equal(t, code, status.Code(native.Unwrap()))
			require.Len(t, stub.requests, 2)
			if code == codes.Canceled {
				require.ErrorIs(t, e, context.Canceled)
			}
			if code == codes.DeadlineExceeded {
				require.ErrorIs(t, e, context.DeadlineExceeded)
			}
		})
	}
}
func TestExactSubjectRelationWirePresence(t *testing.T) {
	for _, tuple := range []string{"document:d#viewer@user:a", "document:d#viewer@group:g#member"} {
		r, e := rel.FromTuple(tuple)
		require.NoError(t, e)
		wire, e := filter(r.Filter())
		require.NoError(t, e)
		require.NotNil(t, wire.OptionalSubjectFilter.OptionalRelation)
		require.Equal(t, r.Subject().Relation(), wire.OptionalSubjectFilter.OptionalRelation.Relation)
	}
	f := testFilter("document").WithSubjectType("user")
	wire, e := filter(f)
	require.NoError(t, e)
	require.Nil(t, wire.OptionalSubjectFilter.OptionalRelation)
}
func TestDeleteCancellationBeforePageRetainsConfirmedProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stub := &deleteStubServer{cancel: cancel, responses: []*v1.DeleteRelationshipsResponse{{DeletedAt: &v1.ZedToken{Token: "r"}, RelationshipsDeletedCount: 3, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL}}}
	c := newTestClient(t, startDeleteStubServer(t, stub))
	r, e := c.DeleteAllRelationshipsNonAtomic(ctx, DeleteAllRequest{Filter: testFilter("document")}, WithDeleteAllTimeout(time.Second))
	require.True(t, errors.Is(e, context.Canceled))
	require.LessOrEqual(t, r.DeletedCount, uint64(3))
	require.Len(t, stub.requests, 1)
}

func TestUnknownDeletionProgressNeverTriggersAnotherMutation(t *testing.T) {
	stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{{DeletionProgress: v1.DeleteRelationshipsResponse_DeletionProgress(999), RelationshipsDeletedCount: 2, DeletedAt: &v1.ZedToken{Token: "ack"}}}}
	c := newTestClient(t, startDeleteStubServer(t, stub))
	r, e := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: testFilter("document")})
	require.Error(t, e)
	require.False(t, r.Complete)
	require.Equal(t, uint64(2), r.DeletedCount)
	require.EqualValues(t, "ack", r.Revision.String())
	require.Len(t, stub.requests, 1)
}

func TestUnknownEmptyDeletionResponseClearsPreviousAcknowledgedCursor(t *testing.T) {
	stub := &deleteStubServer{responses: []*v1.DeleteRelationshipsResponse{
		{DeletedAt: &v1.ZedToken{Token: "previous"}, RelationshipsDeletedCount: 3, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, AfterResultCursor: &v1.Cursor{Token: "previous-cursor"}},
		{DeletionProgress: v1.DeleteRelationshipsResponse_DeletionProgress(999)},
	}}
	c := newTestClient(t, startDeleteStubServer(t, stub))
	defer c.Close()
	result, err := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: testFilter("document")})
	require.Error(t, err)
	require.Equal(t, uint64(3), result.DeletedCount)
	require.Empty(t, result.Cursor)
	require.Empty(t, result.Revision.String())
	require.False(t, result.Complete)
	var progress *DeleteProgressError
	require.ErrorAs(t, err, &progress)
	require.Equal(t, result, progress.Progress)
	require.Len(t, stub.requests, 2)
	require.Equal(t, "previous-cursor", stub.requests[1].OptionalCursor.GetToken())
}
