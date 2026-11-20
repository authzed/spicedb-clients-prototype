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
)

const (
	lookupHas         = v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
	lookupConditional = v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION
)

// batchingServer answers LookupResources the way SpiceDB does: it serves its
// results from the request's cursor (the decimal count of results before it)
// up to the request's limit, packs them into batches of the requested size,
// flushing the last partial batch at the end of the stream, and answers with
// one resource per response when no batch size is requested or when singular
// is set (a server that predates batching).
type batchingServer struct {
	v1.UnimplementedPermissionsServiceServer
	mu       sync.Mutex
	results  []v1.LookupPermissionship
	singular bool
	requests []*v1.LookupResourcesRequest
	messages []int // responses sent per request
}

func (s *batchingServer) LookupResources(req *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.messages = append(s.messages, 0)
	n := len(s.messages) - 1
	s.mu.Unlock()
	send := func(resp *v1.LookupResourcesResponse) error {
		resp.LookedUpAt = &v1.ZedToken{Token: "rev"}
		s.mu.Lock()
		s.messages[n]++
		s.mu.Unlock()
		return stream.Send(resp)
	}
	start, _ := strconv.Atoi(req.GetOptionalCursor().GetToken())
	end := len(s.results)
	if req.GetOptionalLimit() > 0 {
		end = min(end, start+int(req.GetOptionalLimit()))
	}
	size := int(req.GetOptionalResponseBatchSize())
	if s.singular {
		size = 0
	}
	var batch []*v1.LookupResourcesResult
	for i := start; i < end; i++ {
		if size == 0 {
			if err := send(&v1.LookupResourcesResponse{ResourceObjectId: fmt.Sprint("r", i), Permissionship: s.results[i], AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(i + 1)}}); err != nil {
				return err
			}
			continue
		}
		batch = append(batch, &v1.LookupResourcesResult{ResourceObjectId: fmt.Sprint("r", i), Permissionship: s.results[i]})
		if len(batch) == size || i == end-1 {
			if err := send(&v1.LookupResourcesResponse{Batched: &v1.LookupResourcesBatch{Results: batch, AfterCursor: &v1.Cursor{Token: fmt.Sprint(i + 1)}}}); err != nil {
				return err
			}
			batch = nil
		}
	}
	return nil
}

func granted(n int) []v1.LookupPermissionship {
	out := make([]v1.LookupPermissionship, n)
	for i := range out {
		out[i] = lookupHas
	}
	return out
}

func newBatchingClient(t *testing.T, s *batchingServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}

func collectLookup(t *testing.T, c *Client, opts ...LookupResourcesOption) (ids, cursors []string) {
	t.Helper()
	for r, err := range c.LookupResources(context.Background(), lookupReq(), opts...) {
		require.NoError(t, err)
		require.Equal(t, "rev", r.LookedUpAt.String())
		ids = append(ids, r.ResourceID)
		cursors = append(cursors, r.AfterResultCursor)
	}
	return ids, cursors
}

func resourceIDs(from, to int) []string {
	var out []string
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprint("r", i))
	}
	return out
}

func (s *batchingServer) batchSizes() []uint32 {
	var out []uint32
	for _, r := range s.requests {
		out = append(out, r.GetOptionalResponseBatchSize())
	}
	return out
}

func TestLookupResourcesRequestsBatchedResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []LookupResourcesOption
		want uint32
	}{
		{"default page size", nil, defaultLookupPageSize},
		{"small page size", []LookupResourcesOption{WithLookupResourcesPageSize(50)}, 50},
		{"large page size is capped", []LookupResourcesOption{WithLookupResourcesPageSize(2000)}, 1000},
		{"explicit size", []LookupResourcesOption{WithLookupResourcesResponseBatchSize(7)}, 7},
		{"explicit size is not clamped", []LookupResourcesOption{WithLookupResourcesResponseBatchSize(5000)}, 5000},
		{"disabled", []LookupResourcesOption{WithLookupResourcesBatching(BatchingDisabled)}, 0},
		{"disabled wins over a size", []LookupResourcesOption{WithLookupResourcesResponseBatchSize(7), WithLookupResourcesBatching(BatchingDisabled)}, 0},
		{"single request", []LookupResourcesOption{WithLookupResourcesPaging(PagingSingleRequest)}, 1000},
		{"single request with limit", []LookupResourcesOption{WithLookupResourcesPaging(PagingSingleRequest), WithLookupResourcesLimit(40)}, 40},
		{"single request with large limit", []LookupResourcesOption{WithLookupResourcesPaging(PagingSingleRequest), WithLookupResourcesLimit(4000)}, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &batchingServer{results: granted(3)}
			ids, _ := collectLookup(t, newBatchingClient(t, s), tc.opts...)
			require.Equal(t, resourceIDs(0, 3), ids)
			require.Equal(t, []uint32{tc.want}, s.batchSizes())
			require.Equal(t, tc.want, ResolveOptions(tc.opts...).ResponseBatchSize, "the resolved options report the same size")
		})
	}
}

func TestLookupResourcesBatchOptionsValidateLocally(t *testing.T) {
	s := &batchingServer{results: granted(3)}
	c := newBatchingClient(t, s)
	for _, opt := range []LookupResourcesOption{WithLookupResourcesResponseBatchSize(0), WithLookupResourcesBatching(LookupBatching(9))} {
		count := 0
		for _, err := range c.LookupResources(context.Background(), lookupReq(), opt) {
			require.ErrorIs(t, err, ErrInvalidArgument)
			count++
		}
		require.Equal(t, 1, count)
	}
	require.Empty(t, s.requests, "no request is sent")
}

func TestResolveOptionsReportsLookupBatching(t *testing.T) {
	r := ResolveOptions[LookupResourcesOption]()
	require.Equal(t, BatchingAutomatic, r.LookupBatching)
	require.EqualValues(t, defaultLookupPageSize, r.ResponseBatchSize)
	r = ResolveOptions(WithLookupResourcesBatching(BatchingDisabled))
	require.Equal(t, BatchingDisabled, r.LookupBatching)
	require.Zero(t, r.ResponseBatchSize)
	require.Error(t, ResolveOptions(WithLookupResourcesResponseBatchSize(0)).Err)
	require.Zero(t, ResolveOptions[ReadRelationshipsOption]().ResponseBatchSize, "only resource lookups batch")
}

func TestLookupResourcesExpandsBatchesInOrder(t *testing.T) {
	s := &batchingServer{results: granted(7)}
	s.results[1] = lookupConditional
	ids, cursors := collectLookup(t, newBatchingClient(t, s), WithLookupResourcesResponseBatchSize(3))
	require.Equal(t, resourceIDs(0, 7), ids)
	// Only the last item of each batch carries the batch's cursor.
	require.Equal(t, []string{"", "", "3", "", "", "6", "7"}, cursors)
	require.Equal(t, []int{3}, s.messages)

	var got []LookupResource
	for r, err := range newBatchingClient(t, s).LookupResources(context.Background(), lookupReq(), WithLookupResourcesResponseBatchSize(3)) {
		require.NoError(t, err)
		got = append(got, r)
	}
	require.Equal(t, PermissionshipHasPermission, got[0].Permissionship)
	require.Equal(t, PermissionshipConditionalPermission, got[1].Permissionship)
}

func TestLookupResourcesPagesOverBatchesByResultCount(t *testing.T) {
	s := &batchingServer{results: granted(10)}
	ids, cursors := collectLookup(t, newBatchingClient(t, s), WithLookupResourcesPageSize(4))
	require.Equal(t, resourceIDs(0, 10), ids)
	require.Equal(t, []string{"", "", "", "4", "", "", "", "8", "", "10"}, cursors)
	// Each page is one message of four results: the result count, not the
	// message count, says the page is full, and the short third page ends
	// the read.
	require.Equal(t, []int{1, 1, 1}, s.messages)
	require.Len(t, s.requests, 3)
	require.Equal(t, "", s.requests[0].GetOptionalCursor().GetToken())
	require.Equal(t, "4", s.requests[1].GetOptionalCursor().GetToken())
	require.Equal(t, "8", s.requests[2].GetOptionalCursor().GetToken())
	for _, r := range s.requests {
		require.EqualValues(t, 4, r.GetOptionalLimit())
		require.EqualValues(t, 4, r.GetOptionalResponseBatchSize())
	}

	// A batch size that does not divide the page: the page's last, partial
	// batch still ends on the page boundary.
	s = &batchingServer{results: granted(10)}
	ids, cursors = collectLookup(t, newBatchingClient(t, s), WithLookupResourcesPageSize(5), WithLookupResourcesResponseBatchSize(3))
	require.Equal(t, resourceIDs(0, 10), ids)
	require.Equal(t, []string{"", "", "3", "", "5", "", "", "8", "", "10"}, cursors)
	require.Equal(t, []int{2, 2, 0}, s.messages)

	// Resuming from a batch-end cursor returns the rest.
	s = &batchingServer{results: granted(10)}
	ids, _ = collectLookup(t, newBatchingClient(t, s), WithLookupResourcesPageSize(4), WithLookupResourcesCursor("4"))
	require.Equal(t, resourceIDs(4, 10), ids)
}

func TestLookupResourcesReadsSingularResponsesToABatchedRequest(t *testing.T) {
	s := &batchingServer{results: granted(5), singular: true}
	ids, cursors := collectLookup(t, newBatchingClient(t, s), WithLookupResourcesPageSize(2))
	require.Equal(t, resourceIDs(0, 5), ids)
	require.Equal(t, []string{"1", "2", "3", "4", "5"}, cursors)
	require.Equal(t, []uint32{2, 2, 2}, s.batchSizes())
}

func TestLookupResourcesDisabledBatchingKeepsPerItemCursors(t *testing.T) {
	s := &batchingServer{results: granted(3)}
	_, cursors := collectLookup(t, newBatchingClient(t, s), WithLookupResourcesBatching(BatchingDisabled))
	require.Equal(t, []string{"1", "2", "3"}, cursors)
}

func TestLookupResourcesFilterMovesBatchCursorToLastKeptItem(t *testing.T) {
	// Batches of three: [has, has, conditional] [conditional x3] [has, conditional].
	s := &batchingServer{results: []v1.LookupPermissionship{
		lookupHas, lookupHas, lookupConditional,
		lookupConditional, lookupConditional, lookupConditional,
		lookupHas, lookupConditional,
	}}
	ids, cursors := collectLookup(t, newBatchingClient(t, s),
		WithLookupResourcesPermissionshipFilter(LookupHasPermissionOnly), WithLookupResourcesPageSize(3))
	require.Equal(t, []string{"r0", "r1", "r6"}, ids)
	// The dropped trailing result moves its batch's cursor to the last kept
	// item; the wholly dropped batch gives its cursor to no item, and the
	// next page still resumes after it.
	require.Equal(t, []string{"", "3", "8"}, cursors)
	require.Len(t, s.requests, 3)
	require.Equal(t, "3", s.requests[1].GetOptionalCursor().GetToken())
	require.Equal(t, "6", s.requests[2].GetOptionalCursor().GetToken())
}

func TestLookupResourcesLimitEndsOnAnItemWithACursor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       []LookupResourcesOption
		limit      int
		batchSizes []uint32
	}{
		{"remaining below the batch size", []LookupResourcesOption{WithLookupResourcesPageSize(10)}, 25, []uint32{10, 10, 5}},
		{"remaining a multiple of the batch size", []LookupResourcesOption{WithLookupResourcesPageSize(50), WithLookupResourcesResponseBatchSize(10)}, 20, []uint32{10}},
		{"remaining neither", []LookupResourcesOption{WithLookupResourcesPageSize(50), WithLookupResourcesResponseBatchSize(10)}, 25, []uint32{0}},
		{"limit with the unconditional-only filter", []LookupResourcesOption{WithLookupResourcesPageSize(10), WithLookupResourcesPermissionshipFilter(LookupHasPermissionOnly)}, 25, []uint32{0, 0, 0}},
		{"limit with batching disabled", []LookupResourcesOption{WithLookupResourcesPageSize(10), WithLookupResourcesBatching(BatchingDisabled)}, 25, []uint32{0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &batchingServer{results: granted(100)}
			opts := append(tc.opts, WithLookupResourcesLimit(uint64(tc.limit)))
			ids, cursors := collectLookup(t, newBatchingClient(t, s), opts...)
			require.Equal(t, resourceIDs(0, tc.limit), ids)
			require.Equal(t, fmt.Sprint(tc.limit), cursors[len(cursors)-1], "the limited call's last item carries a cursor")
			require.Equal(t, tc.batchSizes, s.batchSizes())
			pageSize := ResolveOptions(opts...).PageSize
			for _, r := range s.requests {
				require.Equal(t, pageSize, r.GetOptionalLimit(), "the wire limit stays the page size")
			}

			// That cursor resumes an unlimited call exactly after the limit.
			rest := &batchingServer{results: granted(100)}
			ids, _ = collectLookup(t, newBatchingClient(t, rest), append(tc.opts, WithLookupResourcesCursor(cursors[len(cursors)-1]))...)
			require.Equal(t, resourceIDs(tc.limit, 100), ids)
		})
	}
}

func TestLookupResourcesMalformedBatchResults(t *testing.T) {
	good := &v1.LookupResourcesResult{ResourceObjectId: "a", Permissionship: lookupHas}
	for _, tc := range []struct {
		name string
		bad  *v1.LookupResourcesResult
		want string
	}{
		{"missing resource id", &v1.LookupResourcesResult{Permissionship: lookupHas}, "resource object id is missing"},
		{"unset permissionship", &v1.LookupResourcesResult{ResourceObjectId: "b"}, "permissionship is unset"},
	} {
		for _, filter := range []LookupPermissionshipFilter{LookupAll, LookupHasPermissionOnly} {
			t.Run(fmt.Sprint(tc.name, "/", filter), func(t *testing.T) {
				stub := &lookupStubServer{lookupResourcesResponses: []*v1.LookupResourcesResponse{{Batched: &v1.LookupResourcesBatch{
					Results: []*v1.LookupResourcesResult{good, tc.bad}, AfterCursor: &v1.Cursor{Token: "2"}}}}}
				c := newTestClient(t, startLookupStubServer(t, stub))
				var ids []string
				var failure error
				for r, err := range c.LookupResources(context.Background(), lookupReq(), WithLookupResourcesPermissionshipFilter(filter)) {
					if err != nil {
						failure = err
						continue
					}
					ids = append(ids, r.ResourceID)
				}
				require.Equal(t, []string{"a"}, ids, "results before the malformed one are yielded, as on a singular stream")
				require.ErrorIs(t, failure, ErrInternal)
				require.ErrorContains(t, failure, "client detected malformed response from server")
				require.ErrorContains(t, failure, tc.want)
			})
		}
	}
}

func TestLookupResourcesBatchEdgeCases(t *testing.T) {
	// An empty batch yields nothing, and a permissionship newer than the
	// client maps to the safe unknown instead of failing.
	stub := &lookupStubServer{lookupResourcesResponses: []*v1.LookupResourcesResponse{
		{Batched: &v1.LookupResourcesBatch{}},
		{Batched: &v1.LookupResourcesBatch{Results: []*v1.LookupResourcesResult{
			{ResourceObjectId: "a", Permissionship: lookupHas},
			{ResourceObjectId: "b", Permissionship: v1.LookupPermissionship(99)},
		}, AfterCursor: &v1.Cursor{Token: "2"}}},
		{Batched: &v1.LookupResourcesBatch{}},
	}}
	c := newTestClient(t, startLookupStubServer(t, stub))
	var got []LookupResource
	for r, err := range c.LookupResources(context.Background(), lookupReq()) {
		require.NoError(t, err)
		got = append(got, r)
	}
	require.Len(t, got, 2)
	require.Equal(t, PermissionshipUnspecified, got[1].Permissionship)
	require.Empty(t, got[0].AfterResultCursor)
	require.Equal(t, "2", got[1].AfterResultCursor)
}
