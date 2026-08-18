package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// The tests in this file prove RULE: Abandoning a stream must release it
// (root DESIGN.md) for every server-streaming iterator the client exposes,
// plus ImportRelationships, the one client-streaming call.
//
// They deliberately assert on a *server-side* signal rather than on the
// consuming loop having exited. A test that only checks the range loop
// terminated passes trivially whether or not the stream was released: the
// consumer side always returns on `break`. What distinguishes a released
// stream from a leaked one is whether SpiceDB was ever told, and the only
// place that is observable is the server's own stream context. So each
// handler below parks on stream.Context().Done() after sending its first
// response -- if the client fails to cancel, the handler never wakes and the
// test fails on the timeout instead of quietly passing.

// streamCancelSignal is the server-side observation shared by the stub
// servers below: closed the moment a handler's stream context is done.
type streamCancelSignal struct {
	cancelled chan struct{}
}

func newStreamCancelSignal() *streamCancelSignal {
	return &streamCancelSignal{cancelled: make(chan struct{})}
}

// awaitCancel blocks until the server's view of the stream is cancelled,
// then records it. Returning the context error mirrors what a real handler
// would do when its client walks away.
func (s *streamCancelSignal) awaitCancel(ctx context.Context) error {
	<-ctx.Done()
	close(s.cancelled)
	return ctx.Err()
}

// requireCancelled fails the test unless the server observed the
// cancellation within a generous bound.
func (s *streamCancelSignal) requireCancelled(t *testing.T) {
	t.Helper()
	select {
	case <-s.cancelled:
	case <-time.After(10 * time.Second):
		t.Fatal("server never observed the stream being cancelled: abandoning the iterator leaked the gRPC stream and the server-side dispatch")
	}
}

// hangingPermissionsServer sends one result on each streaming RPC and then
// waits for its stream to be cancelled.
type hangingPermissionsServer struct {
	v1.UnimplementedPermissionsServiceServer

	sig *streamCancelSignal
}

func (s *hangingPermissionsServer) LookupResources(_ *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	if err := stream.Send(&v1.LookupResourcesResponse{
		ResourceObjectId: "doc1",
		Permissionship:   v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
	}); err != nil {
		return err
	}
	return s.sig.awaitCancel(stream.Context())
}

func (s *hangingPermissionsServer) LookupSubjects(_ *v1.LookupSubjectsRequest, stream grpc.ServerStreamingServer[v1.LookupSubjectsResponse]) error {
	if err := stream.Send(&v1.LookupSubjectsResponse{
		Subject: &v1.ResolvedSubject{
			SubjectObjectId: "user1",
			Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
		},
	}); err != nil {
		return err
	}
	return s.sig.awaitCancel(stream.Context())
}

func (s *hangingPermissionsServer) ReadRelationships(_ *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	if err := stream.Send(&v1.ReadRelationshipsResponse{
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "doc1"},
			Relation: "viewer",
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "user1"}},
		},
	}); err != nil {
		return err
	}
	return s.sig.awaitCancel(stream.Context())
}

func (s *hangingPermissionsServer) ExportBulkRelationships(_ *v1.ExportBulkRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ExportBulkRelationshipsResponse]) error {
	if err := stream.Send(&v1.ExportBulkRelationshipsResponse{
		Relationships: []*v1.Relationship{{
			Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "doc1"},
			Relation: "viewer",
			Subject:  &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "user1"}},
		}},
	}); err != nil {
		return err
	}
	return s.sig.awaitCancel(stream.Context())
}

// ImportBulkRelationships is the client-streaming counterpart: this test
// drives it with a caveat context that fails ToProto before any batch is
// flushed, so the server never receives a message at all. A blocked Recv
// unblocking with an error is itself the proof of release here -- if the
// client instead returned without cancelling, Recv would block for the
// life of the test and the test would time out rather than fail fast.
func (s *hangingPermissionsServer) ImportBulkRelationships(stream grpc.ClientStreamingServer[v1.ImportBulkRelationshipsRequest, v1.ImportBulkRelationshipsResponse]) error {
	if _, err := stream.Recv(); err != nil {
		close(s.sig.cancelled)
		return err
	}
	// A batch arrived (not exercised by the test below, but handled so this
	// stub is not silently wrong if a future test sends one): fall back to
	// waiting on the stream context the same way the server-streaming
	// handlers do.
	return s.sig.awaitCancel(stream.Context())
}

// hangingWatchServer is the WatchService counterpart: one event, then park.
type hangingWatchServer struct {
	v1.UnimplementedWatchServiceServer

	sig *streamCancelSignal
}

func (s *hangingWatchServer) Watch(_ *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	if err := stream.Send(&v1.WatchResponse{
		ChangesThrough: &v1.ZedToken{Token: "token-1"},
	}); err != nil {
		return err
	}
	return s.sig.awaitCancel(stream.Context())
}

// startHangingStreamServer starts an in-process gRPC server exposing both
// the PermissionsService and WatchService stubs above, and returns a client
// pointed at it plus the shared cancellation signal.
func startHangingStreamServer(t *testing.T) (*Client, *streamCancelSignal) {
	t.Helper()

	sig := newStreamCancelSignal()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, &hangingPermissionsServer{sig: sig})
	v1.RegisterWatchServiceServer(grpcServer, &hangingWatchServer{sig: sig})

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	return c, sig
}

// TestUpdates_BreakReleasesTheStream proves that breaking out of a watch
// range loop tells the server to stop, rather than leaving an HTTP/2 stream
// and a server-side dispatch open for the life of the connection.
//
// The context passed to Updates is deliberately long-lived and never
// cancelled by the test, because that is the case that used to leak: the
// caller's own ctx outlives the loop, so only a context derived inside the
// iterator can release the stream on `break`.
func TestUpdates_BreakReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	ctx := context.Background()

	var seen int
	for event, err := range c.Updates(ctx, []string{"document"}, "") {
		require.NoError(t, err)
		require.Equal(t, "token-1", event.ChangesThrough)
		seen++
		break
	}
	require.Equal(t, 1, seen)

	sig.requireCancelled(t)
}

// TestLookupResources_BreakReleasesTheStream proves the same for the
// auto-paging LookupResources iterator.
func TestLookupResources_BreakReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	var seen int
	for res, err := range c.LookupResources(context.Background(), consistency.MinLatency(), "document", "view", "user", "user1") {
		require.NoError(t, err)
		require.Equal(t, "doc1", res.ResourceID)
		seen++
		break
	}
	require.Equal(t, 1, seen)

	sig.requireCancelled(t)
}

// TestLookupSubjects_BreakReleasesTheStream proves the same for
// LookupSubjects, which streams every result in a single call and so can
// hold a dispatch open the longest.
func TestLookupSubjects_BreakReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	var seen int
	for sub, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		require.NoError(t, err)
		require.Equal(t, "user1", sub.Subject.SubjectID)
		seen++
		break
	}
	require.Equal(t, 1, seen)

	sig.requireCancelled(t)
}

// TestReadRelationships_BreakReleasesTheStream proves the same for the
// auto-paging ReadRelationships iterator.
func TestReadRelationships_BreakReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	var seen int
	for r, err := range c.ReadRelationships(context.Background(), consistency.MinLatency(), rel.NewFilter("document")) {
		require.NoError(t, err)
		require.Equal(t, "doc1", r.ResourceID)
		seen++
		break
	}
	require.Equal(t, 1, seen)

	sig.requireCancelled(t)
}

// TestExportRelationships_BreakReleasesTheStream proves the same for the
// bulk export iterator, whose streams are the most expensive to leak
// because a full export holds a server-side snapshot open.
func TestExportRelationships_BreakReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	var seen int
	for r, err := range c.ExportRelationships(context.Background(), consistency.MinLatency(), nil) {
		require.NoError(t, err)
		require.Equal(t, "doc1", r.ResourceID)
		seen++
		break
	}
	require.Equal(t, 1, seen)

	sig.requireCancelled(t)
}

// TestImportRelationships_ToProtoFailureReleasesTheStream proves the
// client-streaming counterpart of the same bug: a relationship whose caveat
// context fails to convert to protobuf used to return immediately from
// ImportRelationships, leaving the client-streaming call opened by
// ImportBulkRelationships neither cancelled, closed, nor drained. The
// caller's own ctx is deliberately long-lived and never cancelled by the
// test, matching every other test in this file, because that is the case
// that used to leak.
func TestImportRelationships_ToProtoFailureReleasesTheStream(t *testing.T) {
	c, sig := startHangingStreamServer(t)

	bad := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("only_bizhours", map[string]any{"offender": make(chan int)})

	numLoaded, err := c.ImportRelationships(context.Background(), func(yield func(rel.Relationship) bool) {
		yield(bad)
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.Zero(t, numLoaded)

	sig.requireCancelled(t)
}
