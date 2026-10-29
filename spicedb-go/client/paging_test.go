package client

import (
	"context"
	"fmt"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"sync"
	"testing"
	"time"
)

type pagingServer struct {
	mu sync.Mutex
	v1.UnimplementedPermissionsServiceServer
	t            *testing.T
	reads        []*v1.ReadRelationshipsRequest
	lookups      []*v1.LookupResourcesRequest
	exports      []*v1.ExportBulkRelationshipsRequest
	delDeadlines []time.Time
	stall        bool
}

func (s *pagingServer) ReadRelationships(r *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	s.mu.Lock()
	s.reads = append(s.reads, r)
	n := len(s.reads)
	s.mu.Unlock()
	if s.stall && n > 1 {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	for i := uint32(0); i < r.OptionalLimit; i++ {
		p, _ := relationship(testTriple("document", fmt.Sprintf("%d-%d", n, i), "viewer", "user", "alice", ""))
		if e := stream.Send(&v1.ReadRelationshipsResponse{Relationship: p, ReadAt: &v1.ZedToken{Token: "pinned"}, AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(n)}}); e != nil {
			return e
		}
	}
	if s.stall {
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}
func (s *pagingServer) LookupResources(r *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	s.mu.Lock()
	s.lookups = append(s.lookups, r)
	n := len(s.lookups)
	s.mu.Unlock()
	for i := uint32(0); i < r.OptionalLimit; i++ {
		if e := stream.Send(&v1.LookupResourcesResponse{ResourceObjectId: fmt.Sprint(i), LookedUpAt: &v1.ZedToken{Token: "pinned"}, Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(n)}}); e != nil {
			return e
		}
	}
	return nil
}
func (s *pagingServer) ExportBulkRelationships(r *v1.ExportBulkRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ExportBulkRelationshipsResponse]) error {
	s.mu.Lock()
	s.exports = append(s.exports, r)
	n := len(s.exports)
	s.mu.Unlock()
	out := &v1.ExportBulkRelationshipsResponse{AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(n)}}
	for i := uint32(0); i < r.OptionalLimit; i++ {
		p, _ := relationship(testTriple("document", fmt.Sprint(i), "viewer", "user", "alice", ""))
		out.Relationships = append(out.Relationships, p)
	}
	return stream.Send(out)
}
func (s *pagingServer) DeleteRelationships(ctx context.Context, r *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	d, ok := ctx.Deadline()
	require.True(s.t, ok)
	s.mu.Lock()
	s.delDeadlines = append(s.delDeadlines, d)
	n := len(s.delDeadlines)
	s.mu.Unlock()
	if n > 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	time.Sleep(30 * time.Millisecond)
	return &v1.DeleteRelationshipsResponse{RelationshipsDeletedCount: 1, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, DeletedAt: &v1.ZedToken{Token: "ack"}}, nil
}
func pagingClient(t *testing.T, s *pagingServer) *Client {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
}
func TestPagedStreamsRespectPageSizeTotalLimitAndRepeatCursorArguments(t *testing.T) {
	s := &pagingServer{t: t}
	c := pagingClient(t, s)
	count := 0
	for _, e := range c.ReadRelationships(context.Background(), ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}, WithReadRelationshipsPageSize(2), WithReadRelationshipsLimit(3)) {
		require.NoError(t, e)
		count++
	}
	require.Equal(t, 3, count)
	require.Len(t, s.reads, 2)
	// SpiceDB rejects a cursor whose call arguments (consistency and limit
	// included) differ from the call that issued it, so every page repeats
	// them; the cursor pins the revision.
	require.Equal(t, uint32(2), s.reads[0].OptionalLimit)
	require.Equal(t, uint32(2), s.reads[1].OptionalLimit)
	require.True(t, s.reads[1].Consistency.GetFullyConsistent())
	require.Equal(t, "1", s.reads[1].OptionalCursor.Token)
	count = 0
	for _, e := range c.LookupResources(context.Background(), LookupResourcesRequest{ResourceType: "document", Permission: "view", Subject: testSubject("user", "alice", ""), Consistency: consistency.Full()}, WithLookupResourcesPageSize(2), WithLookupResourcesLimit(3)) {
		require.NoError(t, e)
		count++
	}
	require.Equal(t, 3, count)
	require.Len(t, s.lookups, 2)
	require.Equal(t, uint32(2), s.lookups[1].OptionalLimit)
	require.True(t, s.lookups[1].Consistency.GetFullyConsistent())
	count = 0
	for _, e := range c.ExportRelationships(context.Background(), ExportRelationshipsRequest{Consistency: consistency.Full()}, WithExportRelationshipsPageSize(2), WithExportRelationshipsLimit(3)) {
		require.NoError(t, e)
		count++
	}
	require.Equal(t, 3, count)
	require.Len(t, s.exports, 2)
	require.Equal(t, uint32(1), s.exports[1].OptionalLimit)
	require.Equal(t, "1", s.exports[1].OptionalCursor.Token)
}
func TestPagedReadAndDeleteAllShareDeadlineAcrossPages(t *testing.T) {
	s := &pagingServer{t: t, stall: true}
	c := pagingClient(t, s)
	var readErr error
	for _, e := range c.ReadRelationships(context.Background(), ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}, WithReadRelationshipsPageSize(2), WithReadRelationshipsTimeout(80*time.Millisecond)) {
		if e != nil {
			readErr = e
		}
	}
	require.ErrorIs(t, readErr, context.DeadlineExceeded)
	s.mu.Lock()
	require.Len(t, s.reads, 2)
	s.mu.Unlock()
	progress, e := c.DeleteAllRelationshipsNonAtomic(context.Background(), DeleteAllRequest{Filter: testFilter("document")}, WithDeleteAllTimeout(80*time.Millisecond))
	require.ErrorIs(t, e, context.DeadlineExceeded)
	require.Equal(t, uint64(1), progress.DeletedCount)
	require.EqualValues(t, "ack", progress.Revision.String())
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.delDeadlines, 2)
	require.WithinDuration(t, s.delDeadlines[0], s.delDeadlines[1], 10*time.Millisecond)
}

type scriptedReceiver struct {
	values []int
	err    error
}

func (s *scriptedReceiver) Recv() (int, error) {
	if len(s.values) > 0 {
		v := s.values[0]
		s.values = s.values[1:]
		return v, nil
	}
	return 0, s.err
}
func TestStreamsRetryOnlyBeforeExposure(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			calls := 0
			items := 0
			errors := 0
			for _, e := range receive(context.Background(), retrySettings{policy: DefaultRetryPolicy(), streams: true}, "test", true, func(context.Context) (receiver[int], error) {
				calls++
				if partial {
					return &scriptedReceiver{values: []int{1}, err: status.Error(codes.Unavailable, "after item")}, nil
				}
				return &scriptedReceiver{err: status.Error(codes.Aborted, "opening")}, nil
			}) {
				if e != nil {
					errors++
				} else {
					items++
				}
			}
			require.Equal(t, 1, errors)
			if partial {
				require.Equal(t, 1, calls)
				require.Equal(t, 1, items)
			} else {
				require.Equal(t, 4, calls)
				require.Zero(t, items)
			}
		})
	}
}
