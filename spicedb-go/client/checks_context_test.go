package client

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// checkCapturingServer's CheckBulkPermissions records every request it
// receives (by value, via proto) and returns a HAS_PERMISSION pair for each
// item, so tests can assert on exactly what was built and sent on the wire.
type checkCapturingServer struct {
	v1.UnimplementedPermissionsServiceServer

	requests []*v1.CheckBulkPermissionsRequest
}

func (s *checkCapturingServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	s.requests = append(s.requests, req)

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

// startCheckCapturingServer starts an in-process gRPC server backed by
// bufconn whose CheckBulkPermissions always succeeds and records the request
// it received, and returns the server (for inspecting captured requests) and
// a dialer for it.
func startCheckCapturingServer(t *testing.T) (*checkCapturingServer, func(context.Context, string) (net.Conn, error)) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &checkCapturingServer{}
	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return srv, func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

func newCapturingTestClient(t *testing.T) (*Client, *checkCapturingServer) {
	t.Helper()

	srv, dialer := startCheckCapturingServer(t)
	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	return c, srv
}

// asMap converts a *structpb.Struct to a plain map for assertions, treating
// a nil Struct as a nil map (no context field set on the wire).
func asMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// wantMap round-trips a map[string]any through structpb the same way
// production code does, so numeric types line up with what AsMap() returns
// (e.g. int 42 becomes float64 42) instead of failing on a type mismatch
// that has nothing to do with the behavior under test.
func wantMap(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if m == nil {
		return nil
	}
	s, err := structpb.NewStruct(m)
	require.NoError(t, err)
	return s.AsMap()
}

// C1: call-level context alone reaches every item in a bulk request.
func TestCheck_CallLevelContextReachesEveryItem(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "")

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view",
		[]rel.Relationship{r1, r2},
		WithCheckContext(map[string]any{"now": 42}),
	)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	want := wantMap(t, map[string]any{"now": 42})
	require.Equal(t, want, asMap(items[0].GetContext()), "item 0 should carry the call-level context")
	require.Equal(t, want, asMap(items[1].GetContext()), "item 1 should carry the call-level context")
}

// C2: per-item context alone reaches only that item.
func TestCheck_PerItemContextReachesOnlyThatItem(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "").
		WithCheckContext(map[string]any{"now": 42})

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view",
		[]rel.Relationship{r1, r2},
	)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	require.Nil(t, asMap(items[0].GetContext()), "item 0 has no per-item context and no call-level default, so no context field should be set")
	require.Equal(t, wantMap(t, map[string]any{"now": 42}), asMap(items[1].GetContext()), "item 1 should carry only its own per-item context")
}

// C3: the merge rule. Call-level {now: 42, region: "us"} + item-level
// {region: "eu"} produces {now: 42, region: "eu"} for that item, and
// {now: 42, region: "us"} (the call-level default, untouched) for a sibling
// item that supplied none. Both items are asserted: asserting only the
// overriding item would not prove the sibling retained the default.
func TestCheck_MergesCallLevelAndPerItemContext(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	sibling := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	overridden := rel.MustFromTriple("document", "2", "view", "user", "bob", "").
		WithCheckContext(map[string]any{"region": "eu"})

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view",
		[]rel.Relationship{sibling, overridden},
		WithCheckContext(map[string]any{"now": 42, "region": "us"}),
	)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	require.Equal(t, wantMap(t, map[string]any{"now": 42, "region": "us"}), asMap(items[0].GetContext()),
		"sibling item supplied no per-item context, so it must retain the call-level default unchanged")
	require.Equal(t, wantMap(t, map[string]any{"now": 42, "region": "eu"}), asMap(items[1].GetContext()),
		"overridden item's region key must win, but the call-level now key (absent from the item) must be retained")
}

// C4: neither call-level nor per-item context supplied => no context field
// set on the wire.
func TestCheck_NoContextSuppliedSetsNoContextField(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "")

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view",
		[]rel.Relationship{r1, r2},
	)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	require.Nil(t, items[0].GetContext())
	require.Nil(t, items[1].GetContext())
}
