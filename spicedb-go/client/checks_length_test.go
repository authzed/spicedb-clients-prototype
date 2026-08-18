package client

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// bulkCheckShortResponseServer returns a fixed number of pairs regardless of
// how many items the request carried, simulating a server that answers fewer
// (or more) checks than were asked. The proto guarantees pairs come back in
// request order but says nothing about count.
type bulkCheckShortResponseServer struct {
	v1.UnimplementedPermissionsServiceServer

	// pairCount is how many pairs to return, ignoring the request's item
	// count entirely.
	pairCount int
	// malformed, when true, returns pairs whose `response` oneof is unset.
	malformed bool
}

func (s *bulkCheckShortResponseServer) CheckBulkPermissions(_ context.Context, _ *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	pairs := make([]*v1.CheckBulkPermissionsPair, s.pairCount)
	for i := range pairs {
		if s.malformed {
			// Neither Item nor Error set — the oneof left empty.
			pairs[i] = &v1.CheckBulkPermissionsPair{}
			continue
		}
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

func startBulkCheckShortResponseServer(t *testing.T, s *bulkCheckShortResponseServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, s)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

func threeRels() []rel.Relationship {
	return []rel.Relationship{
		rel.MustFromTriple("document", "1", "viewer", "user", "alice", ""),
		rel.MustFromTriple("document", "2", "viewer", "user", "alice", ""),
		rel.MustFromTriple("document", "3", "viewer", "user", "alice", ""),
	}
}

// TestCheck_ShortResponseIsRejected proves CheckWithContext refuses a
// response carrying fewer pairs than the request carried items, instead of
// returning a shorter slice whose indices no longer line up with rs.
//
// The proto guarantees pairs are returned in request order but says nothing
// about count, so a short response silently desyncs results[i] from rs[i]
// for every item after the gap — one resource's answer attributed to
// another. This is the guard the other five clients received; Go and
// TypeScript were still sizing results off the response.
func TestCheck_ShortResponseIsRejected(t *testing.T) {
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{pairCount: 2})
	c := newTestClient(t, dialer)

	results, err := c.Check(context.Background(), consistency.MinLatency(), "view", threeRels()...)

	require.Error(t, err)
	require.Nil(t, results)
	require.Contains(t, err.Error(), "2 pair(s)")
	require.Contains(t, err.Error(), "3 request item(s)")

	var nativeErr *Error
	require.True(t, errors.As(err, &nativeErr), "expected a native *Error, got %T", err)
	require.Equal(t, CodeInternal, nativeErr.Code)
}

// TestCheck_LongResponseIsRejected is the mirror case: more pairs than items
// is equally a protocol violation and equally misaligning.
func TestCheck_LongResponseIsRejected(t *testing.T) {
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{pairCount: 5})
	c := newTestClient(t, dialer)

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view", threeRels()...)

	require.Error(t, err)
	require.Contains(t, err.Error(), "5 pair(s)")
	require.Contains(t, err.Error(), "3 request item(s)")
}

// TestCheckOne_ZeroPairResponseReturnsErrorNotPanic proves the specific
// crash the guard removes: CheckOneWithContext does `return results[0], nil`,
// so a zero-pair response used to panic with index-out-of-range inside the
// client, in the caller's goroutine. This is the same panic Task 9 fixed in
// Rust, which was still live in Go.
func TestCheckOne_ZeroPairResponseReturnsErrorNotPanic(t *testing.T) {
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{pairCount: 0})
	c := newTestClient(t, dialer)

	require.NotPanics(t, func() {
		_, err := c.CheckOne(context.Background(), consistency.MinLatency(), "view",
			rel.MustFromTriple("document", "1", "viewer", "user", "alice", ""))
		require.Error(t, err)
		require.Contains(t, err.Error(), "0 pair(s)")
		require.Contains(t, err.Error(), "1 request item(s)")
	})
}

// TestCheckAll_ShortResponseReturnsErrorNotVacuousTrue proves CheckAll no
// longer reports a grant when the server dropped the very checks that would
// have denied. Before the guard, aggregating over a short slice returned
// true because the denying checks simply weren't in it.
func TestCheckAll_ShortResponseReturnsErrorNotVacuousTrue(t *testing.T) {
	// Every returned pair says HAS_PERMISSION, but only one of the three
	// requested checks came back. Without the guard, CheckAll would loop over
	// that single granted result and return true for all three.
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{pairCount: 1})
	c := newTestClient(t, dialer)

	ok, err := c.CheckAll(context.Background(), consistency.MinLatency(), "view", threeRels()...)

	require.Error(t, err)
	require.False(t, ok, "a short response must never aggregate to a grant")
}

// TestCheck_MalformedPairIsRejected proves a CheckBulkPermissionsPair with
// neither Item nor Error set is rejected rather than falling through to the
// item's zero value, which reads as PERMISSIONSHIP_UNSPECIFIED and is
// indistinguishable from a genuine "no permission" answer. Mirrors
// spicedb-rust's malformed-oneof guard.
func TestCheck_MalformedPairIsRejected(t *testing.T) {
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{
		pairCount: 3,
		malformed: true,
	})
	c := newTestClient(t, dialer)

	_, err := c.Check(context.Background(), consistency.MinLatency(), "view", threeRels()...)

	require.Error(t, err)
	require.Contains(t, err.Error(), "malformed CheckBulkPermissionsPair")

	var nativeErr *Error
	require.True(t, errors.As(err, &nativeErr))
	require.Equal(t, CodeInternal, nativeErr.Code)
}

// TestCheck_MatchingLengthStillSucceeds is the companion happy path: a
// response with exactly as many pairs as items is unaffected by the guard.
func TestCheck_MatchingLengthStillSucceeds(t *testing.T) {
	dialer := startBulkCheckShortResponseServer(t, &bulkCheckShortResponseServer{pairCount: 3})
	c := newTestClient(t, dialer)

	results, err := c.Check(context.Background(), consistency.MinLatency(), "view", threeRels()...)

	require.NoError(t, err)
	require.Len(t, results, 3)
	for i, r := range results {
		require.True(t, r.HasPermission(), "result %d", i)
	}
}
