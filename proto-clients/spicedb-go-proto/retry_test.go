package spicedbgoproto

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// flakyPermissionsServer fails the first two CheckPermission calls with
// codes.Unavailable and succeeds on the third, counting attempts.
type flakyPermissionsServer struct {
	v1.UnimplementedPermissionsServiceServer

	attempts atomic.Int32
}

func (s *flakyPermissionsServer) CheckPermission(_ context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	attempt := s.attempts.Add(1)
	if attempt <= 2 {
		return nil, status.Error(codes.Unavailable, "transiently unavailable")
	}

	return &v1.CheckPermissionResponse{
		Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
	}, nil
}

// startFlakyServer starts an in-process gRPC server backed by bufconn and
// returns a dialer for it plus the server for attempt-count assertions.
func startFlakyServer(t *testing.T) (func(context.Context, string) (net.Conn, error), *flakyPermissionsServer) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &flakyPermissionsServer{}

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}

	return dialer, srv
}

// TestNewClient_RetriesTransientErrors proves that NewClient's connection
// automatically retries transient (UNAVAILABLE) errors: a server that fails
// the first two attempts and succeeds on the third should still yield a
// successful CheckPermission call, with the server observing 3 attempts.
func TestNewClient_RetriesTransientErrors(t *testing.T) {
	dialer, srv := startFlakyServer(t)

	client, err := NewClient("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	resp, err := client.PermissionsServiceClient.CheckPermission(context.Background(), &v1.CheckPermissionRequest{
		Resource: &v1.ObjectReference{
			ObjectType: "document",
			ObjectId:   "1",
		},
		Permission: "view",
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{
				ObjectType: "user",
				ObjectId:   "alice",
			},
		},
	})
	require.NoError(t, err, "expected the call to ultimately succeed after retries")
	require.Equal(t, v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, resp.Permissionship)

	require.EqualValues(t, 3, srv.attempts.Load(), "expected the server to observe 3 attempts (1 initial + 2 retries)")
}

// alwaysFailPermissionsServer fails every WriteRelationships/CheckPermission
// call with the given status, counting attempts of each.
type alwaysFailPermissionsServer struct {
	v1.UnimplementedPermissionsServiceServer

	failWith error

	writeAttempts atomic.Int32
	checkAttempts atomic.Int32
}

func (s *alwaysFailPermissionsServer) WriteRelationships(_ context.Context, _ *v1.WriteRelationshipsRequest) (*v1.WriteRelationshipsResponse, error) {
	s.writeAttempts.Add(1)
	return nil, s.failWith
}

func (s *alwaysFailPermissionsServer) CheckPermission(_ context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	s.checkAttempts.Add(1)
	return nil, s.failWith
}

// startAlwaysFailServer starts an in-process gRPC server backed by bufconn,
// serving srv, and returns a dialer for it.
func startAlwaysFailServer(t *testing.T, srv *alwaysFailPermissionsServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestNewClient_MutationIsAttemptedExactlyOnceOnRetryableError proves the
// per-method service-config split: WriteRelationships is method-overridden
// to carry no retryPolicy, so a retryable (UNAVAILABLE) failure is NOT
// retried, even though the same code on CheckPermission is (see
// TestNewClient_RetriesTransientErrors above). A WriteRelationships
// containing OPERATION_CREATE or preconditions is not idempotent -- if it
// commits and the response is lost, a retry would surface
// ALREADY_EXISTS/FAILED_PRECONDITION for a write that in fact succeeded.
func TestNewClient_MutationIsAttemptedExactlyOnceOnRetryableError(t *testing.T) {
	srv := &alwaysFailPermissionsServer{failWith: status.Error(codes.Unavailable, "transiently unavailable")}
	dialer := startAlwaysFailServer(t, srv)

	client, err := NewClient("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	_, err = client.PermissionsServiceClient.WriteRelationships(context.Background(), &v1.WriteRelationshipsRequest{})
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.EqualValues(t, 1, srv.writeAttempts.Load(), "a mutation must be attempted exactly once, even on a retryable error")
}

// TestNewClient_ResourceExhaustedIsNeverRetried proves RESOURCE_EXHAUSTED is
// absent from retryableStatusCodes: unlike UNAVAILABLE, a RESOURCE_EXHAUSTED
// CheckPermission (a read, which otherwise retries freely) is attempted only
// once. In SpiceDB RESOURCE_EXHAUSTED signals memory load-shed or a
// deterministic MaxDepthExceeded, never a transient hiccup.
func TestNewClient_ResourceExhaustedIsNeverRetried(t *testing.T) {
	srv := &alwaysFailPermissionsServer{failWith: status.Error(codes.ResourceExhausted, "quota")}
	dialer := startAlwaysFailServer(t, srv)

	client, err := NewClient("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	_, err = client.PermissionsServiceClient.CheckPermission(context.Background(), &v1.CheckPermissionRequest{
		Resource:   &v1.ObjectReference{ObjectType: "document", ObjectId: "1"},
		Permission: "view",
		Subject:    &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}},
	})
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.EqualValues(t, 1, srv.checkAttempts.Load(), "RESOURCE_EXHAUSTED must never be retried, even on a read")
}

// alwaysFailExperimentalServer fails every BulkImportRelationships call with
// the given status as soon as the stream is opened, counting attempts.
type alwaysFailExperimentalServer struct {
	v1.UnimplementedExperimentalServiceServer

	failWith error

	bulkImportAttempts atomic.Int32
}

func (s *alwaysFailExperimentalServer) BulkImportRelationships(_ v1.ExperimentalService_BulkImportRelationshipsServer) error {
	s.bulkImportAttempts.Add(1)
	return s.failWith
}

// startAlwaysFailExperimentalServer starts an in-process gRPC server backed
// by bufconn, serving srv, and returns a dialer for it.
func startAlwaysFailExperimentalServer(t *testing.T, srv *alwaysFailExperimentalServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterExperimentalServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestNewClient_DeprecatedBulkImportIsAttemptedExactlyOnceOnRetryableError
// proves the fix for the review finding on the first round of this task:
// ExperimentalService.BulkImportRelationships -- the deprecated RPC
// ImportBulkRelationships superseded, but still present on the wire and
// still reachable directly through the exported ExperimentalServiceClient --
// is excluded from the retry policy exactly like its replacement. Before the
// fix, this RPC inherited the service-wide retryPolicy (it wasn't in the
// method-level no-retry entry), so a retryable failure after the transaction
// had already committed server-side would be silently retried and could
// surface a spurious duplicate-relationship error to a caller who in fact
// succeeded.
func TestNewClient_DeprecatedBulkImportIsAttemptedExactlyOnceOnRetryableError(t *testing.T) {
	srv := &alwaysFailExperimentalServer{failWith: status.Error(codes.Unavailable, "transiently unavailable")}
	dialer := startAlwaysFailExperimentalServer(t, srv)

	client, err := NewClient("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	stream, err := client.ExperimentalServiceClient.BulkImportRelationships(context.Background())
	require.NoError(t, err, "opening a client-streaming call does not itself contact the server")

	_, err = stream.CloseAndRecv()
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.EqualValues(t, 1, srv.bulkImportAttempts.Load(), "the deprecated bulk-import RPC must be attempted exactly once, even on a retryable error")
}
