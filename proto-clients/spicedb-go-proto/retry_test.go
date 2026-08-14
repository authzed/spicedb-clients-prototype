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
