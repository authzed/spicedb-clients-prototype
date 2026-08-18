package client

import (
	"context"
	"net"
	"slices"
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

	_, err := c.CheckWithContext(context.Background(), consistency.MinLatency(), "view",
		map[string]any{"now": 42}, r1, r2)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	want := wantMap(t, map[string]any{"now": 42})
	require.Equal(t, want, asMap(items[0].GetContext()), "item 0 should carry the call-level context")
	require.Equal(t, want, asMap(items[1].GetContext()), "item 1 should carry the call-level context")
}

// C2: per-item context alone reaches only that item. Uses the plain,
// non-context Check (variadic, no call-level context argument) to prove
// per-item override works even through the delegating, backward-compatible
// entry point.
func TestCheck_PerItemContextReachesOnlyThatItem(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "").
		WithCheckContext(map[string]any{"now": 42})

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view", r1, r2)
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

	_, err := c.CheckWithContext(context.Background(), consistency.MinLatency(), "view",
		map[string]any{"now": 42, "region": "us"}, sibling, overridden)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	require.Equal(t, wantMap(t, map[string]any{"now": 42, "region": "us"}), asMap(items[0].GetContext()),
		"sibling item supplied no per-item context, so it must retain the call-level default unchanged")
	require.Equal(t, wantMap(t, map[string]any{"now": 42, "region": "eu"}), asMap(items[1].GetContext()),
		"overridden item's region key must win, but the call-level now key (absent from the item) must be retained")
}

// C4: neither call-level nor per-item context supplied via CheckWithContext
// (an explicit nil call-level context) => no context field set on the wire.
func TestCheck_NoContextSuppliedSetsNoContextField(t *testing.T) {
	c, srv := newCapturingTestClient(t)

	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "")

	_, err := c.CheckWithContext(context.Background(), consistency.MinLatency(), "view", nil, r1, r2)
	require.NoError(t, err)
	require.Len(t, srv.requests, 1)

	items := srv.requests[0].GetItems()
	require.Len(t, items, 2)

	require.Nil(t, items[0].GetContext())
	require.Nil(t, items[1].GetContext())
}

// Regression guard for the ergonomics decision: the non-context methods
// (Check, CheckAny, CheckAll, CheckOne, CheckIter) must keep their exact
// pre-existing variadic call shape — no slice literal, no options — and
// must still set no context field on the wire when nothing supplies one.
func TestNonContextMethods_StayVariadicAndSetNoContext(t *testing.T) {
	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "")

	t.Run("Check", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.Check(context.Background(), consistency.MinLatency(), "view", r1, r2)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Nil(t, item.GetContext())
		}
	})

	t.Run("CheckOne", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckOne(context.Background(), consistency.MinLatency(), "view", r1)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		require.Nil(t, srv.requests[0].GetItems()[0].GetContext())
	})

	t.Run("CheckAny", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckAny(context.Background(), consistency.MinLatency(), "view", r1, r2)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Nil(t, item.GetContext())
		}
	})

	t.Run("CheckAll", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckAll(context.Background(), consistency.MinLatency(), "view", r1, r2)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Nil(t, item.GetContext())
		}
	})

	t.Run("CheckIter", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		var count int
		for result, err := range c.CheckIter(context.Background(), consistency.MinLatency(), "view", slices.Values([]rel.Relationship{r1, r2})) {
			require.NoError(t, err)
			require.True(t, result.HasPermission())
			count++
		}
		require.Equal(t, 2, count)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Nil(t, item.GetContext())
		}
	})
}

// Delegation sanity: the WithContext variants of CheckOne/CheckAny/CheckAll/
// CheckIter must actually forward checkContext through to the wire, not
// just accept the parameter and drop it.
func TestWithContextVariants_ForwardCallLevelContextToWire(t *testing.T) {
	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "bob", "")
	want := map[string]any{"now": 7}

	t.Run("CheckOneWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckOneWithContext(context.Background(), consistency.MinLatency(), "view", want, r1)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		require.Equal(t, wantMap(t, want), asMap(srv.requests[0].GetItems()[0].GetContext()))
	})

	t.Run("CheckAnyWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckAnyWithContext(context.Background(), consistency.MinLatency(), "view", want, r1, r2)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Equal(t, wantMap(t, want), asMap(item.GetContext()))
		}
	})

	t.Run("CheckAllWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		_, err := c.CheckAllWithContext(context.Background(), consistency.MinLatency(), "view", want, r1, r2)
		require.NoError(t, err)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Equal(t, wantMap(t, want), asMap(item.GetContext()))
		}
	})

	t.Run("CheckIterWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		seq := slices.Values([]rel.Relationship{r1, r2})
		var count int
		for result, err := range c.CheckIterWithContext(context.Background(), consistency.MinLatency(), "view", want, seq) {
			require.NoError(t, err)
			require.True(t, result.HasPermission())
			count++
		}
		require.Equal(t, 2, count)
		require.Len(t, srv.requests, 1)
		for _, item := range srv.requests[0].GetItems() {
			require.Equal(t, wantMap(t, want), asMap(item.GetContext()))
		}
	})
}

// unconvertibleContext is a caveat context value that structpb.NewStruct
// cannot represent (channels have no protobuf Value encoding), used to prove
// that a conversion failure is surfaced as an error rather than silently
// dropped from the outgoing request.
func unconvertibleContext() map[string]any {
	return map[string]any{"bad": make(chan int)}
}

// TestCheck_UnconvertibleContextReturnsError proves that every check surface
// taking caveat context reports an error — instead of silently sending the
// request with no context field — when the merged context cannot be
// converted to a protobuf Struct. Before the fix, checkItemFromRel discarded
// structpb.NewStruct's error and proceeded with item.Context left nil, so
// the server would come back with a Conditional result the caller could not
// distinguish from "the server legitimately needed more than I supplied".
func TestCheck_UnconvertibleContextReturnsError(t *testing.T) {
	r1 := rel.MustFromTriple("document", "1", "view", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "2", "view", "user", "alice", "")
	bad := unconvertibleContext()

	assertRejected := func(t *testing.T, srv *checkCapturingServer, err error) {
		t.Helper()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrInvalidArgument)
		nativeErr, ok := err.(*Error)
		require.True(t, ok, "expected the conversion failure to be a native *Error, got %T: %v", err, err)
		require.Equal(t, CodeInvalidArgument, nativeErr.Code)
		require.Empty(t, srv.requests, "request must not be sent once context conversion fails")
	}

	t.Run("CheckWithContext call-level", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		results, err := c.CheckWithContext(context.Background(), consistency.MinLatency(), "view", bad, r1)
		require.Nil(t, results)
		assertRejected(t, srv, err)
	})

	t.Run("CheckWithContext per-item", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		results, err := c.CheckWithContext(context.Background(), consistency.MinLatency(), "view", nil, r1.WithCheckContext(bad))
		require.Nil(t, results)
		assertRejected(t, srv, err)
	})

	t.Run("CheckOneWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		result, err := c.CheckOneWithContext(context.Background(), consistency.MinLatency(), "view", bad, r1)
		require.Equal(t, CheckResult{}, result)
		assertRejected(t, srv, err)
	})

	t.Run("CheckAnyWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		got, err := c.CheckAnyWithContext(context.Background(), consistency.MinLatency(), "view", bad, r1, r2)
		require.False(t, got)
		assertRejected(t, srv, err)
	})

	t.Run("CheckAllWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		got, err := c.CheckAllWithContext(context.Background(), consistency.MinLatency(), "view", bad, r1, r2)
		require.False(t, got)
		assertRejected(t, srv, err)
	})

	t.Run("CheckIterWithContext", func(t *testing.T) {
		c, srv := newCapturingTestClient(t)
		seq := slices.Values([]rel.Relationship{r1, r2})
		var gotErr error
		var count int
		for result, err := range c.CheckIterWithContext(context.Background(), consistency.MinLatency(), "view", bad, seq) {
			count++
			require.Equal(t, CheckResult{}, result)
			gotErr = err
		}
		require.Equal(t, 1, count, "CheckIterWithContext must yield exactly one (zero-value, error) pair, not silently skip the batch")
		assertRejected(t, srv, gotErr)
	})
}
