package client

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// bulkCheckErrorServer's CheckBulkPermissions returns a single pair carrying
// a per-item error status, simulating a partial failure within an otherwise
// successful bulk-check RPC.
type bulkCheckErrorServer struct {
	v1.UnimplementedPermissionsServiceServer

	pairErrCode codes.Code
	pairErrMsg  string
}

func (s *bulkCheckErrorServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	pairs := make([]*v1.CheckBulkPermissionsPair, len(req.GetItems()))
	for i := range req.GetItems() {
		pairs[i] = &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Error{
				Error: &rpcstatus.Status{
					Code:    int32(s.pairErrCode),
					Message: s.pairErrMsg,
				},
			},
		}
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
}

// startBulkCheckErrorServer starts an in-process gRPC server backed by
// bufconn whose CheckBulkPermissions always returns a per-item error with
// the given code and message, and returns a dialer for it.
func startBulkCheckErrorServer(t *testing.T, code codes.Code, msg string) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, &bulkCheckErrorServer{pairErrCode: code, pairErrMsg: msg})

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestCheck_PerItemErrorIsMappedToNativeError proves that a per-item error
// returned within a CheckBulkPermissions response pair (a google.rpc.Status,
// not a top-level gRPC error) is mapped through mapGRPCError just like a
// top-level RPC error, so callers can use errors.Is against the native
// sentinels instead of string-matching pair.GetError().GetMessage().
func TestCheck_PerItemErrorIsMappedToNativeError(t *testing.T) {
	dialer := startBulkCheckErrorServer(t, codes.InvalidArgument, "bad resource id")

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		// bufnet is an in-memory bufconn dial target, not a real network
		// destination -- unrelated to what this test exercises.
		WithInsecureAllowRemoteHost(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	_, gotErr := c.CheckOne(context.Background(), consistency.MinLatency(),
		"view", rel.MustFromTriple("document", "1", "viewer", "user", "alice", ""))

	require.Error(t, gotErr)
	require.True(t, errors.Is(gotErr, ErrInvalidArgument),
		"expected the per-item error to satisfy errors.Is(err, client.ErrInvalidArgument), got: %v", gotErr)

	nativeErr, ok := gotErr.(*Error)
	require.True(t, ok, "expected the per-item error to be a native *Error")
	require.Equal(t, CodeInvalidArgument, nativeErr.Code)
	require.Contains(t, nativeErr.Message, "bad resource id")
}
