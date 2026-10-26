package client

import (
	"context"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/protobuf/proto"
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
	for event, err := range c.Updates(context.Background(), UpdatesRequest{ObjectTypes: []string{"document"}}, WithUpdatesStart(consistency.NewZedToken(""))) {
		require.NoError(t, err)
		got = append(got, event)
	}

	require.Len(t, got, 1)
	require.EqualValues(t, "resume-me", got[0].ChangesThrough.String())
}

// TestUpdates_NoOptionByDefaultRequestsNoUpdateKinds proves that Updates
// without WithIncludeCheckpoints leaves OptionalUpdateKinds empty on the
// wire, preserving the server's backwards-compatible default (relationship
// updates only) instead of silently requesting checkpoints.
func TestUpdates_NoOptionByDefaultRequestsNoUpdateKinds(t *testing.T) {
	stub := &watchStubServer{}
	dialer := startWatchStubServer(t, stub)
	c := newTestClient(t, dialer)

	for range c.Updates(context.Background(), UpdatesRequest{ObjectTypes: nil}, WithUpdatesStart(consistency.NewZedToken(""))) {
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

	for range c.Updates(context.Background(), UpdatesRequest{ObjectTypes: nil}, WithUpdatesStart(consistency.NewZedToken("")), WithUpdatesCheckpoints(CheckpointsIncluded)) {
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
	for event, err := range c.Updates(context.Background(), UpdatesRequest{ObjectTypes: nil}, WithUpdatesStart(consistency.NewZedToken("")), WithUpdatesCheckpoints(CheckpointsIncluded)) {
		require.NoError(t, err)
		got = append(got, event)
	}

	require.Len(t, got, 2)

	require.True(t, got[0].IsCheckpoint)
	require.Empty(t, got[0].Updates)
	require.EqualValues(t, "checkpoint-rev", got[0].ChangesThrough.String())

	require.False(t, got[1].IsCheckpoint)
	require.Len(t, got[1].Updates, 1)
	require.EqualValues(t, "update-rev", got[1].ChangesThrough.String())
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
	for event, err := range c.Updates(context.Background(), UpdatesRequest{ObjectTypes: nil}, WithUpdatesStart(consistency.NewZedToken(""))) {
		yields++
		require.Equal(t, WatchEvent{}, event)
		gotErr = err
	}

	require.Equal(t, 1, yields)
	require.Error(t, gotErr)
}

func TestUpdatesRelationshipFiltersSnapshotAndWireMapping(t *testing.T) {
	stub := &watchStubServer{}
	c := newTestClient(t, startWatchStubServer(t, stub))
	defer c.Close()
	named, err := rel.NamedRelation("member")
	require.NoError(t, err)
	filters := []rel.Filter{
		testFilter("document").WithResourceID("one").WithRelation("viewer").WithSubjectType("user").WithSubjectID("alice").WithSubjectRelation(rel.DirectRelation()),
		testFilter("folder").WithResourceIDPrefix("team-").WithRelation("reader").WithSubjectType("group").WithSubjectID("eng").WithSubjectRelation(named),
	}
	stream := c.Updates(context.Background(), UpdatesRequest{Filters: filters})
	filters[0] = testFilter("mutated")
	for _, err := range stream {
		require.NoError(t, err)
	}
	require.Empty(t, stub.lastRequest.OptionalObjectTypes)
	expected := []*v1.RelationshipFilter{
		{ResourceType: "document", OptionalResourceId: "one", OptionalRelation: "viewer", OptionalSubjectFilter: &v1.SubjectFilter{SubjectType: "user", OptionalSubjectId: "alice", OptionalRelation: &v1.SubjectFilter_RelationFilter{Relation: ""}}},
		{ResourceType: "folder", OptionalResourceIdPrefix: "team-", OptionalRelation: "reader", OptionalSubjectFilter: &v1.SubjectFilter{SubjectType: "group", OptionalSubjectId: "eng", OptionalRelation: &v1.SubjectFilter_RelationFilter{Relation: "member"}}},
	}
	require.Len(t, stub.lastRequest.OptionalRelationshipFilters, len(expected))
	for i, want := range expected {
		require.True(t, proto.Equal(want, stub.lastRequest.OptionalRelationshipFilters[i]))
	}
}

func TestUpdatesRejectsInvalidFiltersBeforeRPC(t *testing.T) {
	for _, request := range []UpdatesRequest{
		{ObjectTypes: []string{"document"}, Filters: []rel.Filter{testFilter("document")}},
		{Filters: []rel.Filter{{}}},
		{Filters: []rel.Filter{testFilter("document").WithSubjectID("alice")}},
	} {
		stub := &watchStubServer{}
		c := newTestClient(t, startWatchStubServer(t, stub))
		for _, err := range c.Updates(context.Background(), request) {
			require.ErrorIs(t, err, ErrInvalidArgument)
		}
		require.Nil(t, stub.lastRequest)
		require.NoError(t, c.Close())
	}
}

func TestUpdatesEmptyFiltersWatchAll(t *testing.T) {
	stub := &watchStubServer{}
	c := newTestClient(t, startWatchStubServer(t, stub))
	defer c.Close()
	for _, err := range c.Updates(context.Background(), UpdatesRequest{Filters: []rel.Filter{}}) {
		require.NoError(t, err)
	}
	require.Empty(t, stub.lastRequest.OptionalRelationshipFilters)
	require.Empty(t, stub.lastRequest.OptionalObjectTypes)
}
