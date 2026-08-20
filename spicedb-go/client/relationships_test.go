package client

import (
	"context"
	"net"
	"testing"

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
}

func (s *deleteStubServer) DeleteRelationships(_ context.Context, req *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	s.requests = append(s.requests, req)

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

// TestDeleteRelationships_NoOptions_PreservesDefaultBehavior proves that
// calling DeleteRelationships with no options is unchanged from before
// DeleteOption existed: default page size, allow-partial-deletions true, and
// no preconditions sent.
func TestDeleteRelationships_NoOptions_PreservesDefaultBehavior(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")

	revision, err := c.DeleteRelationships(context.Background(), filter)
	require.NoError(t, err)
	require.Equal(t, "rev-1", revision)

	require.Len(t, stub.requests, 1)
	req := stub.requests[0]
	filterProto, err := filter.ToProto()
	require.NoError(t, err)
	require.Equal(t, filterProto, req.GetRelationshipFilter())
	require.Equal(t, uint32(defaultDeletePageSize), req.GetOptionalLimit())
	require.True(t, req.GetOptionalAllowPartialDeletions())
	require.Empty(t, req.GetOptionalPreconditions())
}

// TestDeleteRelationships_FilterSubjectIDWithoutSubjectType_ReturnsError
// proves the fix for the offboarding hazard this finding describes: a
// filter carrying a subject ID but no subject type must be rejected before
// it ever reaches the server, not silently sent as an unconstrained-subject
// delete that would remove every relationship on every document.
func TestDeleteRelationships_FilterSubjectIDWithoutSubjectType_ReturnsError(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithSubjectID("alice")

	_, err := c.DeleteRelationships(context.Background(), filter)
	require.Error(t, err)
	require.ErrorIs(t, err, rel.ErrInvalidFilter)
	require.Contains(t, err.Error(), "SubjectID")
	require.Contains(t, err.Error(), "SubjectType")

	require.Empty(t, stub.requests, "no request should reach the server for an unconvertible filter")
}

// TestDeleteRelationships_WithDeleteMustMatchFilterSubjectIDWithoutSubjectType_ReturnsError
// proves the same rejection applies when the unconvertible filter arrives
// via a WithDeleteMustMatch/WithDeleteMustNotMatch precondition option
// rather than as the primary filter -- DeleteOption can't return an error
// directly, so the conversion error must be deferred and surfaced by
// DeleteRelationships itself.
func TestDeleteRelationships_WithDeleteMustMatchFilterSubjectIDWithoutSubjectType_ReturnsError(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")
	badGuard := rel.NewFilter("document").WithSubjectID("alice")

	_, err := c.DeleteRelationships(context.Background(), filter, WithDeleteMustMatch(badGuard))
	require.Error(t, err)
	require.ErrorIs(t, err, rel.ErrInvalidFilter)

	require.Empty(t, stub.requests, "no request should reach the server for an unconvertible precondition filter")
}

// TestDeleteRelationships_WithDeleteMustMatch_SetsMustMatchPrecondition
// proves that WithDeleteMustMatch adds a MUST_MATCH precondition built from
// the supplied filter.
func TestDeleteRelationships_WithDeleteMustMatch_SetsMustMatchPrecondition(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")
	guard := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner").
		WithSubjectType("user").WithSubjectID("alice")

	_, err := c.DeleteRelationships(context.Background(), filter, WithDeleteMustMatch(guard))
	require.NoError(t, err)

	require.Len(t, stub.requests, 1)
	preconditions := stub.requests[0].GetOptionalPreconditions()
	require.Len(t, preconditions, 1)
	require.Equal(t, v1.Precondition_OPERATION_MUST_MATCH, preconditions[0].GetOperation())
	guardProto, err := guard.ToProto()
	require.NoError(t, err)
	require.Equal(t, guardProto, preconditions[0].GetFilter())
}

// TestDeleteRelationships_WithDeleteMustNotMatch_SetsMustNotMatchPrecondition
// proves that WithDeleteMustNotMatch adds a MUST_NOT_MATCH precondition
// built from the supplied filter.
func TestDeleteRelationships_WithDeleteMustNotMatch_SetsMustNotMatchPrecondition(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")
	guard := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("banned").
		WithSubjectType("user").WithSubjectID("mallory")

	_, err := c.DeleteRelationships(context.Background(), filter, WithDeleteMustNotMatch(guard))
	require.NoError(t, err)

	require.Len(t, stub.requests, 1)
	preconditions := stub.requests[0].GetOptionalPreconditions()
	require.Len(t, preconditions, 1)
	require.Equal(t, v1.Precondition_OPERATION_MUST_NOT_MATCH, preconditions[0].GetOperation())
	guardProto, err := guard.ToProto()
	require.NoError(t, err)
	require.Equal(t, guardProto, preconditions[0].GetFilter())
}

// TestDeleteRelationships_MultiplePreconditions_AreAllSent proves that
// multiple WithDeleteMustMatch/WithDeleteMustNotMatch options accumulate
// rather than overwrite each other.
func TestDeleteRelationships_MultiplePreconditions_AreAllSent(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")
	mustMatch := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner")
	mustNotMatch := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("locked")

	_, err := c.DeleteRelationships(context.Background(), filter,
		WithDeleteMustMatch(mustMatch),
		WithDeleteMustNotMatch(mustNotMatch),
	)
	require.NoError(t, err)

	require.Len(t, stub.requests, 1)
	preconditions := stub.requests[0].GetOptionalPreconditions()
	require.Len(t, preconditions, 2)
	require.Equal(t, v1.Precondition_OPERATION_MUST_MATCH, preconditions[0].GetOperation())
	mustMatchProto, err := mustMatch.ToProto()
	require.NoError(t, err)
	require.Equal(t, mustMatchProto, preconditions[0].GetFilter())
	require.Equal(t, v1.Precondition_OPERATION_MUST_NOT_MATCH, preconditions[1].GetOperation())
	mustNotMatchProto, err := mustNotMatch.ToProto()
	require.NoError(t, err)
	require.Equal(t, mustNotMatchProto, preconditions[1].GetFilter())
}

// TestDeleteRelationships_WithDeleteLimit_OverridesDefaultLimit proves that
// WithDeleteLimit overrides the default page size sent as OptionalLimit.
func TestDeleteRelationships_WithDeleteLimit_OverridesDefaultLimit(t *testing.T) {
	stub := &deleteStubServer{}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")

	_, err := c.DeleteRelationships(context.Background(), filter, WithDeleteLimit(42))
	require.NoError(t, err)

	require.Len(t, stub.requests, 1)
	require.Equal(t, uint32(42), stub.requests[0].GetOptionalLimit())
	require.True(t, stub.requests[0].GetOptionalAllowPartialDeletions())
}

// TestDeleteRelationships_AutoPaging_ResendsPreconditionsEveryCall proves
// that when the server reports a PARTIAL deletion, the client repeats the
// call (auto-paging), and that supplied preconditions are re-sent on every
// call in the loop (the proto only carries preconditions per-request, so
// there's no way to "check once" across a multi-call delete).
func TestDeleteRelationships_AutoPaging_ResendsPreconditionsEveryCall(t *testing.T) {
	stub := &deleteStubServer{
		responses: []*v1.DeleteRelationshipsResponse{
			{
				DeletedAt:        &v1.ZedToken{Token: "rev-partial"},
				DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL,
			},
			{
				DeletedAt:        &v1.ZedToken{Token: "rev-complete"},
				DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE,
			},
		},
	}
	dialer := startDeleteStubServer(t, stub)
	c := newTestClient(t, dialer)

	filter := rel.NewFilter("document").WithResourceID("firstdoc")
	guard := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner").
		WithSubjectType("user").WithSubjectID("alice")

	revision, err := c.DeleteRelationships(context.Background(), filter, WithDeleteMustMatch(guard))
	require.NoError(t, err)
	require.Equal(t, "rev-complete", revision)

	guardProto, err := guard.ToProto()
	require.NoError(t, err)

	require.Len(t, stub.requests, 2)
	for i, req := range stub.requests {
		preconditions := req.GetOptionalPreconditions()
		require.Lenf(t, preconditions, 1, "call %d", i)
		require.Equal(t, v1.Precondition_OPERATION_MUST_MATCH, preconditions[0].GetOperation())
		require.Equal(t, guardProto, preconditions[0].GetFilter())
	}
}
