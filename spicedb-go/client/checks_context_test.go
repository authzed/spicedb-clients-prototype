package client

import (
	"context"
	"math"
	"net"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type checkServer struct {
	v1.UnimplementedPermissionsServiceServer
	bulk   func(context.Context, *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error)
	single func(context.Context, *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error)
}

func (s *checkServer) CheckPermission(ctx context.Context, r *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	return s.single(ctx, r)
}
func (s *checkServer) CheckBulkPermissions(ctx context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	return s.bulk(ctx, r)
}
func checkClient(t *testing.T, s *checkServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}
func successfulBulk(r *v1.CheckBulkPermissionsRequest) *v1.CheckBulkPermissionsResponse {
	pairs := make([]*v1.CheckBulkPermissionsPair, len(r.Items))
	for i, item := range r.Items {
		pairs[i] = &v1.CheckBulkPermissionsPair{Request: proto.Clone(item).(*v1.CheckBulkPermissionsRequestItem), Response: &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}}}
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs, CheckedAt: &v1.ZedToken{Token: "revision"}}
}
func TestCheckContextPrecedenceAndSnapshots(t *testing.T) {
	var wire []*v1.CheckBulkPermissionsRequestItem
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		wire = r.Items
		return successfulBulk(r), nil
	}})
	defaults := map[string]any{"region": "us", "nested": map[string]any{"list": []any{nil, true, 42}}}
	option := WithCheckContext(defaults)
	defaults["nested"].(map[string]any)["list"].([]any)[1] = false
	a := testRequest("a")
	a.Context = map[string]any{"region": "eu"}
	b := testRequest("b")
	_, e := c.CheckBulk(context.Background(), []PermissionCheck{a, b}, option)
	require.NoError(t, e)
	require.Equal(t, "eu", wire[0].Context.AsMap()["region"])
	require.Equal(t, "us", wire[1].Context.AsMap()["region"])
	require.Equal(t, []any{nil, true, float64(42)}, wire[0].Context.AsMap()["nested"].(map[string]any)["list"])
	require.Equal(t, "eu", a.Context["region"])
	require.Len(t, a.Context, 1)
	_, e = c.CheckBulk(context.Background(), []PermissionCheck{b})
	require.NoError(t, e)
	require.Nil(t, wire[0].Context)
}
func TestCheckInvalidContextNamesKeyBeforeRPC(t *testing.T) {
	c := &Client{}
	for _, v := range []any{make(chan int), math.Inf(1), math.NaN(), map[string]any{"nested": make(chan int)}} {
		r := testRequest("a")
		r.Context = map[string]any{"offending": v}
		_, e := c.Check(context.Background(), r)
		require.ErrorIs(t, e, ErrInvalidArgument)
		require.Contains(t, e.Error(), "offending")
		_, e = c.CheckBulk(context.Background(), []PermissionCheck{r})
		require.ErrorIs(t, e, ErrInvalidArgument)
		require.Contains(t, e.Error(), "offending")
	}
	_, e := c.CheckBulk(context.Background(), nil, WithCheckContext(map[string]any{"bad": make(chan int)}))
	require.ErrorIs(t, e, ErrInvalidArgument)
}
func TestCheckSingularUsesSingularRPCAndContext(t *testing.T) {
	calls := 0
	c := checkClient(t, &checkServer{single: func(_ context.Context, r *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
		calls++
		require.Equal(t, "eu", r.Context.AsMap()["region"])
		require.Equal(t, true, r.Context.AsMap()["default"])
		require.False(t, r.WithTracing)
		return &v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, CheckedAt: &v1.ZedToken{Token: "single-revision"}}, nil
	}})
	r := testRequest("a")
	r.Context = map[string]any{"region": "eu"}
	result, e := c.Check(context.Background(), r, WithCheckContext(map[string]any{"region": "us", "default": true}))
	require.NoError(t, e)
	require.True(t, result.HasPermission())
	require.EqualValues(t, "single-revision", result.CheckedAt.String())
	require.Nil(t, result.DebugTrace)
	require.Equal(t, 1, calls)
}
