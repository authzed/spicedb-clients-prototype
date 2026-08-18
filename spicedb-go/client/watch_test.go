package client

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// watchStubServer is a stub WatchService whose Watch implementation streams
// fixed, synthetic responses and records the request it received, so tests
// can assert both on how the client maps responses to WatchEvent and on
// what the client actually put on the wire.
type watchStubServer struct {
	v1.UnimplementedWatchServiceServer

	responses   []*v1.WatchResponse
	lastRequest *v1.WatchRequest
}

func (s *watchStubServer) Watch(req *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	s.lastRequest = req
	for _, resp := range s.responses {
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	return nil
}

// startWatchStubServer starts an in-process gRPC server backed by bufconn,
// exposing a WatchService backed by the given stub, and returns a dialer
// for it.
func startWatchStubServer(t *testing.T, stub *watchStubServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterWatchServiceServer(grpcServer, stub)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestUpdates_ExposesUsableResumeToken proves that a watch event surfaces
// the response's changes_through as WatchEvent.ChangesThrough, so a
// consumer whose stream dies can resume from exactly where it left off
// instead of restarting from its original startRevision (reprocessing,
// possibly past the GC window) or from head (silently losing every change
// in the gap).
func TestUpdates_ExposesUsableResumeToken(t *testing.T) {
	stub := &watchStubServer{
		responses: []*v1.WatchResponse{
			{ChangesThrough: &v1.ZedToken{Token: "resume-me"}},
		},
	}
	dialer := startWatchStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []WatchEvent
	for event, err := range c.Updates(context.Background(), []string{"document"}, "") {
		require.NoError(t, err)
		got = append(got, event)
	}

	require.Len(t, got, 1)
	require.Equal(t, "resume-me", got[0].ChangesThrough)
}

// TestUpdates_NoOptionByDefaultRequestsNoUpdateKinds proves that Updates
// without WithIncludeCheckpoints leaves OptionalUpdateKinds empty on the
// wire, preserving the server's backwards-compatible default (relationship
// updates only) instead of silently requesting checkpoints.
func TestUpdates_NoOptionByDefaultRequestsNoUpdateKinds(t *testing.T) {
	stub := &watchStubServer{}
	dialer := startWatchStubServer(t, stub)
	c := newTestClient(t, dialer)

	for range c.Updates(context.Background(), nil, "") {
	}

	require.NotNil(t, stub.lastRequest)
	require.Empty(t, stub.lastRequest.GetOptionalUpdateKinds())
}

// TestUpdates_WithIncludeCheckpointsReachesTheWire proves that
// WithIncludeCheckpoints puts WATCH_KIND_INCLUDE_CHECKPOINTS on the built
// WatchRequest -- asserting on the request the stub server actually
// received, not just that the call succeeded.
func TestUpdates_WithIncludeCheckpointsReachesTheWire(t *testing.T) {
	stub := &watchStubServer{}
	dialer := startWatchStubServer(t, stub)
	c := newTestClient(t, dialer)

	for range c.Updates(context.Background(), nil, "", WithIncludeCheckpoints()) {
	}

	require.NotNil(t, stub.lastRequest)
	require.Contains(t, stub.lastRequest.GetOptionalUpdateKinds(), v1.WatchKind_WATCH_KIND_INCLUDE_CHECKPOINTS)
	// Requesting checkpoints must not silently drop relationship updates --
	// OptionalUpdateKinds is empty-means-default, so a non-empty list is the
	// exact set requested.
	require.Contains(t, stub.lastRequest.GetOptionalUpdateKinds(), v1.WatchKind_WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES)
}

// TestUpdates_CheckpointEventDistinguishableFromUpdateEvent proves that a
// checkpoint event (no updates) is distinguishable from an event carrying
// relationship updates via WatchEvent.IsCheckpoint, so a consumer can tell
// "nothing changed, here is a fresh resume point" from "here are changes".
func TestUpdates_CheckpointEventDistinguishableFromUpdateEvent(t *testing.T) {
	stub := &watchStubServer{
		responses: []*v1.WatchResponse{
			{
				ChangesThrough: &v1.ZedToken{Token: "checkpoint-rev"},
				IsCheckpoint:   true,
			},
			{
				ChangesThrough: &v1.ZedToken{Token: "update-rev"},
				Updates: []*v1.RelationshipUpdate{
					{
						Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
						Relationship: &v1.Relationship{
							Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "1"},
							Relation: "viewer",
							Subject: &v1.SubjectReference{
								Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"},
							},
						},
					},
				},
			},
		},
	}
	dialer := startWatchStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []WatchEvent
	for event, err := range c.Updates(context.Background(), nil, "", WithIncludeCheckpoints()) {
		require.NoError(t, err)
		got = append(got, event)
	}

	require.Len(t, got, 2)

	require.True(t, got[0].IsCheckpoint)
	require.Empty(t, got[0].Updates)
	require.Equal(t, "checkpoint-rev", got[0].ChangesThrough)

	require.False(t, got[1].IsCheckpoint)
	require.Len(t, got[1].Updates, 1)
	require.Equal(t, "update-rev", got[1].ChangesThrough)
}

// TestUpdates_StreamErrorYieldsZeroValueAndMappedError proves that a
// mid-stream error yields a zero-value WatchEvent paired with a native
// mapped error, matching the (result, error) iterator contract used
// elsewhere in the client.
func TestUpdates_StreamErrorYieldsZeroValueAndMappedError(t *testing.T) {
	// startErroringServer (from errors_test.go) only registers
	// PermissionsService, not WatchService, so Watch hits an unregistered
	// service and fails at the transport level -- enough to prove errors
	// flow through mapGRPCError rather than panicking or being silently
	// swallowed.
	dialer := startErroringServer(t)
	c := newTestClient(t, dialer)

	var (
		yields int
		gotErr error
	)
	for event, err := range c.Updates(context.Background(), nil, "") {
		yields++
		require.Equal(t, WatchEvent{}, event)
		gotErr = err
	}

	require.Equal(t, 1, yields)
	require.Error(t, gotErr)
}
