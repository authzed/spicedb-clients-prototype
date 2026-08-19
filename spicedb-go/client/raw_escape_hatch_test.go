package client

import (
	"context"
	"net"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// The escape hatch, RawProto, exists so a request the idiomatic surface cannot
// express has a workaround short of forking the client. Asserting the accessor
// returns something non-nil would prove none of that. What matters is whether a
// caller can drive a generated service client through it and get an answer out
// of a real server, with this client's bearer token attached -- so the tests
// below run a real (in-process, bufconn-backed) gRPC server and assert the
// authorization metadata the handler actually received.
//
// The RPC driven here is CheckPermission, the single-check call the idiomatic
// client never makes (CheckOne routes every check through
// CheckBulkPermissions), so the gap is genuine rather than contrived.

// authRecordingServer implements both the single-check and bulk-check RPCs,
// recording the authorization metadata of every request in arrival order.
type authRecordingServer struct {
	v1.UnimplementedPermissionsServiceServer

	authorizations []string
}

func (s *authRecordingServer) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) == 0 {
		s.authorizations = append(s.authorizations, "")
		return
	}
	s.authorizations = append(s.authorizations, values[0])
}

func (s *authRecordingServer) CheckPermission(ctx context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	s.record(ctx)
	return &v1.CheckPermissionResponse{
		Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
		CheckedAt:      &v1.ZedToken{Token: "rev-raw"},
	}, nil
}

func (s *authRecordingServer) CheckBulkPermissions(ctx context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	s.record(ctx)
	pairs := make([]*v1.CheckBulkPermissionsPair, len(req.GetItems()))
	for i := range req.GetItems() {
		pairs[i] = &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
				},
			},
		}
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
}

func newAuthRecordingTestClient(t *testing.T) (*Client, *authRecordingServer) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &authRecordingServer{}
	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		// bufnet is an in-memory bufconn dial target, not a real network
		// destination -- unrelated to what these tests exercise.
		WithInsecureAllowRemoteHost(),
		WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		})),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c, srv
}

func checkPermissionRequest() *v1.CheckPermissionRequest {
	return &v1.CheckPermissionRequest{
		Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true},
		},
		Resource:   &v1.ObjectReference{ObjectType: "document", ObjectId: "readme"},
		Permission: "view",
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "jimmy"},
		},
	}
}

func TestRawProto_DrivesARealServiceClientAgainstARealServer(t *testing.T) {
	c, srv := newAuthRecordingTestClient(t)

	resp, err := c.RawProto().PermissionsServiceClient.CheckPermission(context.Background(), checkPermissionRequest())
	require.NoError(t, err)
	require.Equal(t, v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, resp.GetPermissionship())
	require.Equal(t, "rev-raw", resp.GetCheckedAt().GetToken())

	// The bearer token rides this client's own connection credentials, so a
	// raw call is authenticated exactly as an idiomatic one is.
	require.Equal(t, []string{"Bearer test-token"}, srv.authorizations)
}

func TestRawProto_SharesTheConnectionTheIdiomaticMethodsUse(t *testing.T) {
	c, srv := newAuthRecordingTestClient(t)

	// Not a second connection built behind the caller's back.
	require.Same(t, c.RawProto(), c.RawProto())

	result, err := c.CheckOne(context.Background(), consistency.Full(), "view",
		rel.MustFromTriple("document", "readme", "view", "user", "jimmy", ""))
	require.NoError(t, err)
	require.True(t, result.HasPermission())

	_, err = c.RawProto().PermissionsServiceClient.CheckPermission(context.Background(), checkPermissionRequest())
	require.NoError(t, err)

	// One idiomatic call (via CheckBulkPermissions) and one raw call (via the
	// single-check RPC), both authenticated, both on this client's own
	// connection.
	require.Equal(t, []string{"Bearer test-token", "Bearer test-token"}, srv.authorizations)
}

// The hatch must never grow into a way to build a connection. Root DESIGN.md,
// "RULE: Credentials over insecure transport require an explicit opt-in", is
// enforced in NewWithOpts, on the single path that dials. Handing back an
// already-built client cannot bypass that; accepting an endpoint, token, or
// transport setting would.
func TestRawProto_IsAnAccessorNotASecondConstructionPath(t *testing.T) {
	method, ok := reflect.TypeOf(&Client{}).MethodByName("RawProto")
	require.True(t, ok, "RawProto must be exported")
	// One parameter: the receiver. Anything more is a construction argument.
	require.Equal(t, 1, method.Type.NumIn(), "RawProto must take no arguments")

	// And the guard still refuses what it always did.
	_, err := NewWithOpts("evil.example.com:50051", "test-token", WithInsecure())
	require.Error(t, err)
	require.Contains(t, err.Error(), "WithInsecureAllowRemoteHost")
}

func TestRawProto_OnAZeroValueClientIsNil(t *testing.T) {
	require.Nil(t, (&Client{}).RawProto())
	var nilClient *Client
	require.Nil(t, nilClient.RawProto())
}
