package client

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// bulkCheckEchoServer answers every item, echoing the item's resource ID
// back through MissingRequiredContext so a caller can prove which request
// item each result came from -- and therefore that concatenating chunk
// responses preserved input order. It records the item count of every
// request it received.
type bulkCheckEchoServer struct {
	v1.UnimplementedPermissionsServiceServer

	mu           sync.Mutex
	requestSizes []int
	// shortAtRequest, when >= 0, makes the request at that index (0-based)
	// return one fewer pair than it was asked for, exercising the
	// per-chunk length guard.
	shortAtRequest int
}

func (s *bulkCheckEchoServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	s.mu.Lock()
	index := len(s.requestSizes)
	s.requestSizes = append(s.requestSizes, len(req.GetItems()))
	s.mu.Unlock()

	items := req.GetItems()
	if s.shortAtRequest == index && len(items) > 0 {
		items = items[:len(items)-1]
	}

	pairs := make([]*v1.CheckBulkPermissionsPair, len(items))
	for i, item := range items {
		pairs[i] = &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
					PartialCaveatInfo: &v1.PartialCaveatInfo{
						MissingRequiredContext: []string{item.GetResource().GetObjectId()},
					},
				},
			},
		}
	}
	return &v1.CheckBulkPermissionsResponse{
		Pairs:     pairs,
		CheckedAt: &v1.ZedToken{Token: "tok"},
	}, nil
}

func (s *bulkCheckEchoServer) sizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.requestSizes))
	copy(out, s.requestSizes)
	return out
}

func startBulkCheckEchoServer(t *testing.T, s *bulkCheckEchoServer) func(context.Context, string) (net.Conn, error) {
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

// numberedRels builds n relationships whose resource IDs are their index,
// zero-padded so lexical and numeric order agree when read by eye.
func numberedRels(n int) []rel.Relationship {
	rs := make([]rel.Relationship, n)
	for i := range rs {
		rs[i] = rel.MustFromTriple("document", fmt.Sprintf("%05d", i), "viewer", "user", "alice", "")
	}
	return rs
}

// TestCheck_SplitsOversizedInputIntoChunks proves Check does not forward an
// unbounded caller slice as one request. SpiceDB rejects a request carrying
// more than maxBulkCheckCount (10,000, a hard-coded const with no flag)
// with ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST, and nothing in the proto
// caps items -- only server code does -- so the client has to chunk.
func TestCheck_SplitsOversizedInputIntoChunks(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: -1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	const total = defaultCheckBatchSize*2 + 7
	results, err := c.Check(context.Background(), consistency.MinLatency(), "view", numberedRels(total)...)

	require.NoError(t, err)
	require.Len(t, results, total)
	require.Equal(t,
		[]int{defaultCheckBatchSize, defaultCheckBatchSize, 7},
		srv.sizes(),
		"expected three requests sized by defaultCheckBatchSize, not one unbounded request")
}

// TestCheck_ChunkedResultsStayInInputOrder proves concatenating chunk
// responses preserves the caller's order across chunk boundaries. The fake
// server echoes each item's resource ID back, so a reordering -- or a
// chunk's results landing under the wrong offset -- is visible on every one
// of the 2,007 results, not just at the seams.
func TestCheck_ChunkedResultsStayInInputOrder(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: -1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	const total = defaultCheckBatchSize*2 + 7
	results, err := c.Check(context.Background(), consistency.MinLatency(), "view", numberedRels(total)...)

	require.NoError(t, err)
	require.Len(t, results, total)
	for i, r := range results {
		require.Equal(t, fmt.Sprintf("%05d", i), r.MissingContext[0],
			"result %d carries the answer for request item %s", i, r.MissingContext[0])
	}
}

// TestCheck_UnderChunkSizeSendsExactlyOneRequest guards the common case
// against regressing into a loop with per-chunk overhead.
func TestCheck_UnderChunkSizeSendsExactlyOneRequest(t *testing.T) {
	for _, n := range []int{1, 999, defaultCheckBatchSize} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			srv := &bulkCheckEchoServer{shortAtRequest: -1}
			c := newTestClient(t, startBulkCheckEchoServer(t, srv))

			results, err := c.Check(context.Background(), consistency.MinLatency(), "view", numberedRels(n)...)

			require.NoError(t, err)
			require.Len(t, results, n)
			require.Equal(t, []int{n}, srv.sizes())
		})
	}
}

// TestCheck_EmptyInputSendsNoRequest proves zero relationships costs zero
// round trips -- not one request carrying an empty item list -- and returns
// an empty result rather than an error.
func TestCheck_EmptyInputSendsNoRequest(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: -1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	results, err := c.Check(context.Background(), consistency.MinLatency(), "view")

	require.NoError(t, err)
	require.Empty(t, results)
	require.Empty(t, srv.sizes(), "an empty input must not reach the wire at all")
}

// TestCheckAll_EmptyInputIsFalseAndSendsNoRequest keeps chunking from
// resurrecting the vacuous-true bug: an aggregate over zero checks is
// false, and it costs no request.
func TestCheckAll_EmptyInputIsFalseAndSendsNoRequest(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: -1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	ok, err := c.CheckAll(context.Background(), consistency.MinLatency(), "view")

	require.NoError(t, err)
	require.False(t, ok, "zero checks is never a grant")
	require.Empty(t, srv.sizes())
}

// TestCheck_LengthGuardFiresOnALaterChunk proves the pair-count guard is
// evaluated per chunk, not once against the caller's total. The first chunk
// answers in full; the second returns 999 pairs for 1,000 items. Without a
// per-chunk guard the shortfall would silently desync every result from the
// second chunk onward.
func TestCheck_LengthGuardFiresOnALaterChunk(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: 1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	const total = defaultCheckBatchSize*2 + 7
	results, err := c.Check(context.Background(), consistency.MinLatency(), "view", numberedRels(total)...)

	require.Error(t, err)
	require.Nil(t, results)
	require.Contains(t, err.Error(), "999 pair(s)")
	require.Contains(t, err.Error(), "1000 request item(s)")
}

// TestCheckIter_StillBatchesAtTheSharedConstant proves reusing
// defaultCheckBatchSize for CheckWithContext's chunking left CheckIter's
// request pattern untouched: it still flushes one request per 1,000
// relationships, and each flush is a single chunk downstream rather than
// being re-split.
func TestCheckIter_StillBatchesAtTheSharedConstant(t *testing.T) {
	srv := &bulkCheckEchoServer{shortAtRequest: -1}
	c := newTestClient(t, startBulkCheckEchoServer(t, srv))

	const total = defaultCheckBatchSize*2 + 7
	rs := numberedRels(total)
	seq := func(yield func(rel.Relationship) bool) {
		for _, r := range rs {
			if !yield(r) {
				return
			}
		}
	}

	var got []CheckResult
	for result, err := range c.CheckIter(context.Background(), consistency.MinLatency(), "view", seq) {
		require.NoError(t, err)
		got = append(got, result)
	}

	require.Len(t, got, total)
	require.Equal(t,
		[]int{defaultCheckBatchSize, defaultCheckBatchSize, 7},
		srv.sizes(),
		"CheckIter must still send one request per batch, not re-chunk them")
	for i, r := range got {
		require.Equal(t, fmt.Sprintf("%05d", i), r.MissingContext[0])
	}
}
