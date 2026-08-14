package client

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// allErrorSentinels lists every sentinel mapGRPCError can produce, used to
// assert that a mapped error matches its own sentinel and no others.
var allErrorSentinels = []error{
	ErrNotFound,
	ErrAlreadyExists,
	ErrInvalidArgument,
	ErrFailedPrecondition,
	ErrPermissionDenied,
	ErrUnauthenticated,
}

func TestMapGRPCError_MapsKnownCodesToSentinels(t *testing.T) {
	cases := []struct {
		name     string
		code     codes.Code
		sentinel error
	}{
		{"NotFound", codes.NotFound, ErrNotFound},
		{"AlreadyExists", codes.AlreadyExists, ErrAlreadyExists},
		{"InvalidArgument", codes.InvalidArgument, ErrInvalidArgument},
		{"FailedPrecondition", codes.FailedPrecondition, ErrFailedPrecondition},
		{"PermissionDenied", codes.PermissionDenied, ErrPermissionDenied},
		{"Unauthenticated", codes.Unauthenticated, ErrUnauthenticated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := status.Error(tc.code, "boom")
			got := mapGRPCError("some op", src)
			require.Error(t, got)

			require.True(t, errors.Is(got, tc.sentinel), "expected errors.Is(got, %v) to be true", tc.sentinel)

			for _, other := range allErrorSentinels {
				if other == tc.sentinel {
					continue
				}
				require.False(t, errors.Is(got, other), "expected errors.Is(got, %v) to be false", other)
			}

			nativeErr, ok := got.(*Error)
			require.True(t, ok, "expected mapGRPCError to return *Error")
			require.Equal(t, tc.code, nativeErr.Code)
			require.Contains(t, nativeErr.Message, "some op")
			require.Contains(t, nativeErr.Message, "boom")

			require.NotNil(t, errors.Unwrap(got))
		})
	}
}

func TestMapGRPCError_NilErrorReturnsNil(t *testing.T) {
	got := mapGRPCError("x", nil)
	require.Nil(t, got)
	require.True(t, got == nil)
}

func TestMapGRPCError_UnmappedCodeMatchesNoSentinel(t *testing.T) {
	src := status.Error(codes.Internal, "boom")
	got := mapGRPCError("some op", src)
	require.Error(t, got)

	nativeErr, ok := got.(*Error)
	require.True(t, ok, "expected mapGRPCError to return *Error")
	require.Equal(t, codes.Internal, nativeErr.Code)

	for _, sentinel := range allErrorSentinels {
		require.False(t, errors.Is(got, sentinel), "codes.Internal should not match sentinel %v", sentinel)
	}
}

// erroringPermissionsServer's ReadRelationships fails immediately with
// codes.NotFound, simulating a server-side error surfaced mid-stream.
type erroringPermissionsServer struct {
	v1.UnimplementedPermissionsServiceServer
}

func (s *erroringPermissionsServer) ReadRelationships(_ *v1.ReadRelationshipsRequest, _ grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	return status.Error(codes.NotFound, "nope")
}

// startErroringServer starts an in-process gRPC server backed by bufconn,
// exposing a PermissionsService whose ReadRelationships always fails with
// codes.NotFound, and returns a dialer for it.
func startErroringServer(t *testing.T) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, &erroringPermissionsServer{})

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestReadRelationships_StreamErrorIsMappedToNativeError proves that errors
// surfaced mid-stream (i.e. from stream.Recv(), not just the initial RPC
// call) are routed through mapGRPCError, so iterator consumers can use
// errors.Is against the native sentinels instead of inspecting raw gRPC
// status codes.
func TestReadRelationships_StreamErrorIsMappedToNativeError(t *testing.T) {
	dialer := startErroringServer(t)

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	var (
		gotErr   error
		yields   int
	)
	for _, iterErr := range c.ReadRelationships(context.Background(), consistency.MinLatency(), rel.NewFilter("document")) {
		yields++
		if iterErr != nil {
			gotErr = iterErr
		}
	}

	require.Equal(t, 1, yields, "expected exactly one yield carrying the mapped error")
	require.Error(t, gotErr)
	require.True(t, errors.Is(gotErr, ErrNotFound), "expected the stream error to satisfy errors.Is(err, client.ErrNotFound), got: %v", gotErr)

	nativeErr, ok := gotErr.(*Error)
	require.True(t, ok, "expected the stream error to be a native *Error")
	require.Equal(t, codes.NotFound, nativeErr.Code)
}
