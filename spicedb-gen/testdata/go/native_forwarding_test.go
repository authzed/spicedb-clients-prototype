package permissions_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type nativeForwardingServer struct {
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedWatchServiceServer
	materialize.UnimplementedRelationshipsServiceServer
	deletes    []*v1.DeleteRelationshipsRequest
	failDelete bool
	watch      chan *v1.WatchRequest
	released   chan struct{}
	imported   []*v1.Relationship
	count      *materialize.ExperimentalCountRelationshipsByFilterRequest
}

func (s *nativeForwardingServer) DeleteRelationships(_ context.Context, r *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	s.deletes = append(s.deletes, r)
	if len(s.deletes) == 2 && s.failDelete {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	complete := v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL
	if len(s.deletes) == 2 {
		complete = v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE
	}
	return &v1.DeleteRelationshipsResponse{DeletedAt: &v1.ZedToken{Token: "ack"}, RelationshipsDeletedCount: 2, DeletionProgress: complete, AfterResultCursor: &v1.Cursor{Token: "next"}}, nil
}
func (s *nativeForwardingServer) Watch(r *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	s.watch <- r
	if err := stream.Send(&v1.WatchResponse{ChangesThrough: &v1.ZedToken{Token: "event"}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	close(s.released)
	return stream.Context().Err()
}
func (s *nativeForwardingServer) ImportBulkRelationships(stream grpc.ClientStreamingServer[v1.ImportBulkRelationshipsRequest, v1.ImportBulkRelationshipsResponse]) error {
	for {
		r, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&v1.ImportBulkRelationshipsResponse{NumLoaded: uint64(len(s.imported))})
		}
		if err != nil {
			return err
		}
		s.imported = append(s.imported, r.Relationships...)
	}
}
func (s *nativeForwardingServer) ExperimentalCountRelationshipsByFilter(_ context.Context, r *materialize.ExperimentalCountRelationshipsByFilterRequest) (*materialize.ExperimentalCountRelationshipsByFilterResponse, error) {
	s.count = r
	return &materialize.ExperimentalCountRelationshipsByFilterResponse{RelationshipCount: 7, ReadAt: &v1.ZedToken{Token: "count"}}, nil
}
func newNativeForwardingClient(t *testing.T, s *nativeForwardingServer) *TypedClient {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(server, s)
	v1.RegisterWatchServiceServer(server, s)
	materialize.RegisterRelationshipsServiceServer(server, s)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	native, err := client.NewWithOpts("passthrough:///bufnet", "token", client.WithInsecure(), client.WithRemotePlaintextCredentials(client.RemotePlaintextAllowed), client.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) })))
	require.NoError(t, err)
	typed := NewTypedClient(native)
	t.Cleanup(func() { require.NoError(t, typed.Close()) })
	require.Same(t, native, typed.Interface)
	return typed
}
func TestTypedNativeDeletionOptionsCursorsAndProgress(t *testing.T) {
	filter, err := rel.NewFilter("document")
	require.NoError(t, err)
	for _, mode := range []string{"bounded", "complete", "failure"} {
		t.Run(mode, func(t *testing.T) {
			stub := &nativeForwardingServer{failDelete: mode == "failure"}
			typed := newNativeForwardingClient(t, stub)
			var result client.DeleteResult
			if mode == "bounded" {
				result, err = typed.DeleteRelationships(context.Background(), client.DeleteRequest{Filter: filter, Limit: 3}, client.WithDeleteCursor("initial"), client.WithDeleteMustMatch(filter))
			} else {
				result, err = typed.DeleteAllRelationshipsNonAtomic(context.Background(), client.DeleteAllRequest{Filter: filter}, client.WithDeleteAllPageSize(3), client.WithDeleteAllCursor("initial"), client.WithDeleteAllMustMatch(filter))
			}
			if mode == "failure" {
				var progress *client.DeleteProgressError
				require.ErrorAs(t, err, &progress)
				require.Equal(t, result, progress.Progress)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "ack", result.Revision.String())
			require.Equal(t, "next", result.Cursor)
			if mode == "complete" {
				require.Equal(t, uint64(4), result.DeletedCount)
				require.True(t, result.Complete)
			} else {
				require.Equal(t, uint64(2), result.DeletedCount)
				require.False(t, result.Complete)
			}
			require.Equal(t, "initial", stub.deletes[0].OptionalCursor.Token)
			for _, r := range stub.deletes {
				require.Equal(t, uint32(3), r.OptionalLimit)
				require.Len(t, r.OptionalPreconditions, 1)
				require.Equal(t, "document", r.RelationshipFilter.ResourceType)
			}
			if mode == "bounded" {
				require.Len(t, stub.deletes, 1)
			} else {
				require.Len(t, stub.deletes, 2)
				require.Equal(t, "next", stub.deletes[1].OptionalCursor.Token)
			}
		})
	}
}
func TestTypedNativeWatchPreservesFiltersAndStreamOwnership(t *testing.T) {
	stub := &nativeForwardingServer{watch: make(chan *v1.WatchRequest, 1), released: make(chan struct{})}
	typed := newNativeForwardingClient(t, stub)
	filter, err := rel.NewFilter("document")
	require.NoError(t, err)
	stream := typed.Updates(context.Background(), client.UpdatesRequest{Filters: []rel.Filter{filter.WithResourceID("one").WithRelation("viewer")}}, client.WithUpdatesStart(consistency.NewZedToken("start")), client.WithUpdatesCheckpoints(client.CheckpointsIncluded))
	select {
	case <-stub.watch:
		t.Fatal("watch must be lazy")
	default:
	}
	for event, err := range stream {
		require.NoError(t, err)
		require.Equal(t, "event", event.ChangesThrough.String())
		break
	}
	request := <-stub.watch
	require.Equal(t, "start", request.OptionalStartCursor.Token)
	require.Len(t, request.OptionalRelationshipFilters, 1)
	require.Equal(t, "one", request.OptionalRelationshipFilters[0].OptionalResourceId)
	require.Equal(t, "viewer", request.OptionalRelationshipFilters[0].OptionalRelation)
	require.Len(t, request.OptionalUpdateKinds, 2)
	select {
	case <-stub.released:
	case <-time.After(time.Second):
		t.Fatal("breaking typed watch leaked native stream")
	}
}
func TestTypedNativeImportAndMaterialize(t *testing.T) {
	stub := &nativeForwardingServer{}
	typed := newNativeForwardingClient(t, stub)
	object, err := rel.NewObject("document", "one")
	require.NoError(t, err)
	user, err := rel.NewObject("user", "alice")
	require.NoError(t, err)
	subject, err := rel.NewSubject(user, "")
	require.NoError(t, err)
	relationship, err := rel.NewRelationship(object, "viewer", subject)
	require.NoError(t, err)
	result, err := typed.ImportRelationships(context.Background(), client.ImportRelationshipsRequest{Relationships: func(yield func(rel.Relationship) bool) { yield(relationship) }}, client.WithImportRelationshipsTimeout(time.Second))
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.ImportedCount)
	require.Equal(t, "one", stub.imported[0].Resource.ObjectId)
	count, err := typed.CountRelationshipsByFilter(context.Background(), client.CountRelationshipsByFilterRequest{Filter: relationship.Filter()}, client.WithCountRelationshipsByFilterTimeout(time.Second))
	require.NoError(t, err)
	require.Equal(t, uint64(7), count.RelationshipCount)
	require.Equal(t, "count", count.Revision.String())
	require.Equal(t, "alice", stub.count.RelationshipFilter.OptionalSubjectFilter.OptionalSubjectId)
}
