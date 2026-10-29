package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// stallServer serves full pages until page stallPage, which sends half a page
// and then waits for its stream to end. pageDelay slows every page without
// stalling it.
type stallServer struct {
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedWatchServiceServer
	stallPage int
	pageDelay time.Duration
	pages     atomic.Int32
}

func (s *stallServer) page(ctx context.Context, limit uint32, send func(i uint32, page int) error) error {
	n := int(s.pages.Add(1))
	for i := uint32(0); i < limit; i++ {
		if n == s.stallPage && i == limit/2 {
			<-ctx.Done()
			return ctx.Err()
		}
		if e := send(i, n); e != nil {
			return e
		}
	}
	time.Sleep(s.pageDelay)
	return nil
}

func (s *stallServer) LookupResources(r *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	return s.page(stream.Context(), r.OptionalLimit, func(i uint32, n int) error {
		return stream.Send(&v1.LookupResourcesResponse{ResourceObjectId: fmt.Sprintf("%d-%d", n, i), Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(n)}})
	})
}

func (s *stallServer) ReadRelationships(r *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	return s.page(stream.Context(), r.OptionalLimit, func(i uint32, n int) error {
		p, _ := relationship(testTriple("document", fmt.Sprintf("%d-%d", n, i), "viewer", "user", "alice", ""))
		return stream.Send(&v1.ReadRelationshipsResponse{Relationship: p, AfterResultCursor: &v1.Cursor{Token: fmt.Sprint(n)}})
	})
}

func (s *stallServer) ExportBulkRelationships(r *v1.ExportBulkRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ExportBulkRelationshipsResponse]) error {
	return s.page(stream.Context(), r.OptionalLimit, func(i uint32, n int) error {
		p, _ := relationship(testTriple("document", fmt.Sprintf("%d-%d", n, i), "viewer", "user", "alice", ""))
		return stream.Send(&v1.ExportBulkRelationshipsResponse{Relationships: []*v1.Relationship{p}, AfterResultCursor: &v1.Cursor{Token: fmt.Sprintf("%d-%d", n, i)}})
	})
}

func (s *stallServer) LookupSubjects(_ *v1.LookupSubjectsRequest, stream grpc.ServerStreamingServer[v1.LookupSubjectsResponse]) error {
	if e := stream.Send(&v1.LookupSubjectsResponse{Subject: &v1.ResolvedSubject{SubjectObjectId: "alice", Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION}}); e != nil {
		return e
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

// Watch sends its only event after pageDelay, longer than the client default.
func (s *stallServer) Watch(_ *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	time.Sleep(s.pageDelay)
	return stream.Send(&v1.WatchResponse{ChangesThrough: &v1.ZedToken{Token: "late"}})
}

func newStallClient(t *testing.T, srv *stallServer, opts ...Option) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(gs, srv)
	v1.RegisterWatchServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	base := []Option{WithInsecure(), WithRemotePlaintextCredentials(RemotePlaintextAllowed), WithRetries(RetriesDisabled),
		WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))}
	c, err := NewWithOpts("passthrough:///bufnet", "t", append(base, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func lookupReq() LookupResourcesRequest {
	return LookupResourcesRequest{ResourceType: "document", Permission: "view", Subject: testSubject("user", "alice", ""), Consistency: consistency.Full()}
}

// drain consumes a paged read, returning the items seen, the error that ended
// it and how long it took.
func drain[T any](seq func(func(T, error) bool)) (int, time.Duration, error) {
	start := time.Now()
	n := 0
	for _, e := range seq {
		if e != nil {
			return n, time.Since(start), e
		}
		n++
	}
	return n, time.Since(start), nil
}

// A server that stalls on page 3 fails the read after the client default
// deadline, with no caller option set, instead of hanging it.
func TestPageDeadline_StalledPageFailsAfterClientDefault(t *testing.T) {
	const def = 200 * time.Millisecond
	cases := map[string]func(c *Client) (int, time.Duration, error){
		"lookup resources": func(c *Client) (int, time.Duration, error) {
			return drain(c.LookupResources(context.Background(), lookupReq(), WithLookupResourcesPageSize(4)))
		},
		"read relationships": func(c *Client) (int, time.Duration, error) {
			return drain(c.ReadRelationships(context.Background(), ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}, WithReadRelationshipsPageSize(4)))
		},
		"export relationships": func(c *Client) (int, time.Duration, error) {
			return drain(c.ExportRelationships(context.Background(), ExportRelationshipsRequest{Consistency: consistency.Full()}, WithExportRelationshipsPageSize(4)))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			c := newStallClient(t, &stallServer{stallPage: 3}, WithDefaultTimeout(def))
			n, took, err := run(c)
			require.Error(t, err)
			require.Equal(t, CodeDeadlineExceeded, CodeOf(err), "%v", err)
			require.True(t, errors.Is(err, ErrDeadlineExceeded))
			require.Equal(t, 10, n, "two full pages and half of the third arrive first")
			require.GreaterOrEqual(t, took, def)
			require.Less(t, took, 5*def)
		})
	}

	t.Run("lookup subjects", func(t *testing.T) {
		c := newStallClient(t, &stallServer{}, WithDefaultTimeout(def))
		n, took, err := drain(c.LookupSubjects(context.Background(), LookupSubjectsRequest{Resource: testObject("document", "1"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()}))
		require.Equal(t, CodeDeadlineExceeded, CodeOf(err), "%v", err)
		require.Equal(t, 1, n)
		require.Less(t, took, 5*def)
	})
}

// The default bounds each page, not the whole read: pages that each finish
// within it add up to more than it and still succeed.
func TestPageDeadline_AppliesPerPageNotPerRead(t *testing.T) {
	c := newStallClient(t, &stallServer{pageDelay: 80 * time.Millisecond}, WithDefaultTimeout(200*time.Millisecond))
	n, took, err := drain(c.LookupResources(context.Background(), lookupReq(), WithLookupResourcesPageSize(2), WithLookupResourcesLimit(10)))
	require.NoError(t, err)
	require.Equal(t, 10, n)
	require.Greater(t, took, 200*time.Millisecond, "five pages outlast one page's default")
}

// A caller's total deadline still bounds the whole read and wins when earlier.
func TestPageDeadline_CallerTotalDeadlineWins(t *testing.T) {
	c := newStallClient(t, &stallServer{stallPage: 3}, WithDefaultTimeout(10*time.Second))
	_, took, err := drain(c.LookupResources(context.Background(), lookupReq(), WithLookupResourcesPageSize(4), WithLookupResourcesTimeout(150*time.Millisecond)))
	require.Equal(t, CodeDeadlineExceeded, CodeOf(err))
	require.Less(t, took, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, took, err = drain(c.LookupResources(ctx, lookupReq(), WithLookupResourcesPageSize(4)))
	require.Equal(t, CodeDeadlineExceeded, CodeOf(err))
	require.Less(t, took, 2*time.Second)
}

// DefaultDeadlineDisabled removes the per-page default; the caller's context
// still applies.
func TestPageDeadline_DisabledDefault(t *testing.T) {
	c := newStallClient(t, &stallServer{stallPage: 3}, WithDefaultTimeout(50*time.Millisecond), WithDefaultDeadline(DefaultDeadlineDisabled))
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, took, err := drain(c.LookupResources(ctx, lookupReq(), WithLookupResourcesPageSize(4)))
	require.Equal(t, CodeDeadlineExceeded, CodeOf(err))
	require.GreaterOrEqual(t, took, 400*time.Millisecond)
}

// Watch keeps no default deadline.
func TestPageDeadline_WatchKeepsNoDefault(t *testing.T) {
	c := newStallClient(t, &stallServer{pageDelay: 300 * time.Millisecond}, WithDefaultTimeout(100*time.Millisecond))
	for ev, err := range c.Updates(context.Background(), UpdatesRequest{}) {
		require.NoError(t, err)
		require.Equal(t, "late", ev.ChangesThrough.String())
		break
	}
}

// Local cancellation surfaces as the client's own cancelled error (with the
// context error still matchable), never as a bare context or transport error:
// before a call, while a page is stalled, and during an import.
func TestLocalCancellationIsClientCanceledError(t *testing.T) {
	requireCanceled := func(t *testing.T, err error) {
		t.Helper()
		var native *Error
		require.True(t, errors.As(err, &native), "want *Error, got %T: %v", err, err)
		require.Equal(t, CodeCanceled, native.Code)
		require.ErrorIs(t, err, ErrCanceled)
		require.ErrorIs(t, err, context.Canceled)
	}
	c := newStallClient(t, &stallServer{stallPage: 1})

	pre, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Check(pre, testRequest("1"))
	requireCanceled(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, _, err = drain(c.LookupResources(ctx, lookupReq(), WithLookupResourcesPageSize(4)))
	requireCanceled(t, err)

	ctx, cancel = context.WithCancel(context.Background())
	_, err = c.ImportRelationships(ctx, ImportRelationshipsRequest{Relationships: func(yield func(rel.Relationship) bool) {
		cancel()
		yield(testTriple("document", "1", "viewer", "user", "alice", ""))
	}})
	requireCanceled(t, err)
}
