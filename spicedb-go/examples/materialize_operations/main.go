// Example materialize_operations exercises the experimental materialize surface
// against a local stand-in. Materialize is a separate service from the SpiceDB
// test container, so its protocol contracts and stream cancellation are explicit.
package main

import (
	"context"
	"fmt"
	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/examples/internal/exampleutil"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log"
	"net"
	"time"
)

type service struct {
	materialize.UnimplementedRelationshipsServiceServer
	materialize.UnimplementedRoaringLookupResourcesServiceServer
	materialize.UnimplementedWatchPermissionSetsServiceServer
	closed chan struct{}
}

func (s *service) ExperimentalCountRelationshipsByFilter(_ context.Context, r *materialize.ExperimentalCountRelationshipsByFilterRequest) (*materialize.ExperimentalCountRelationshipsByFilterResponse, error) {
	if r.RelationshipFilter.ResourceType != "document" {
		log.Fatal("count filter missing")
	}
	return &materialize.ExperimentalCountRelationshipsByFilterResponse{RelationshipCount: 42, ReadAt: &v1.ZedToken{Token: "revision"}}, nil
}
func (s *service) ExperimentalRoaringLookupResources(_ context.Context, r *materialize.ExperimentalRoaringLookupResourcesRequest) (*materialize.ExperimentalRoaringLookupResourcesResponse, error) {
	if !r.Consistency.GetFullyConsistent() || r.Subject.Object.ObjectId != "alice" {
		log.Fatal("roaring request lost identity or consistency")
	}
	return &materialize.ExperimentalRoaringLookupResourcesResponse{Bitmap: []byte{1, 2, 3}, Cardinality: 42, AtRevision: &v1.ZedToken{Token: "revision"}}, nil
}
func (s *service) WatchPermissionSets(_ *materialize.WatchPermissionSetsRequest, stream materialize.WatchPermissionSetsService_WatchPermissionSetsServer) error {
	if e := stream.Send(&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_CompletedRevision{CompletedRevision: &v1.ZedToken{Token: "revision"}}}); e != nil {
		return e
	}
	<-stream.Context().Done()
	close(s.closed)
	return stream.Context().Err()
}
func (s *service) LookupPermissionSets(r *materialize.LookupPermissionSetsRequest, stream materialize.WatchPermissionSetsService_LookupPermissionSetsServer) error {
	if r.Limit != 5 {
		log.Fatal("lookup must preserve bounded limit")
	}
	return stream.Send(&materialize.LookupPermissionSetsResponse{Change: &materialize.PermissionSetChange{Operation: materialize.PermissionSetChange_SET_OPERATION_ADDED, AtRevision: &v1.ZedToken{Token: "revision"}, ParentSet: &materialize.SetReference{ObjectType: "document", ObjectId: "doc", PermissionOrRelation: "view"}, Child: &materialize.PermissionSetChange_ChildMember{ChildMember: &materialize.MemberReference{ObjectType: "user", ObjectId: "alice"}}}, Cursor: &materialize.Cursor{CompletedMembers: true, Token: &v1.ZedToken{Token: "revision"}, Cursor: "opaque"}})
}
func (s *service) DownloadPermissionSets(_ context.Context, r *materialize.DownloadPermissionSetsRequest) (*materialize.DownloadPermissionSetsResponse, error) {
	if r.OptionalAtRevision.Token != "revision" {
		log.Fatal("download revision missing")
	}
	return &materialize.DownloadPermissionSetsResponse{Files: []*materialize.File{{Name: "sets.avro", Url: "https://download.invalid/sets.avro"}}, AtRevision: &v1.ZedToken{Token: "revision"}, Timestamp: timestamppb.New(time.Unix(123, 0))}, nil
}
func main() {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		log.Fatal(e)
	}
	stub := &service{closed: make(chan struct{})}
	server := grpc.NewServer()
	materialize.RegisterRelationshipsServiceServer(server, stub)
	materialize.RegisterRoaringLookupResourcesServiceServer(server, stub)
	materialize.RegisterWatchPermissionSetsServiceServer(server, stub)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	c, e := client.NewPlaintext(listener.Addr().String(), "example-token")
	if e != nil {
		log.Fatal(e)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	count, e := c.CountRelationshipsByFilter(ctx, client.CountRelationshipsByFilterRequest{Filter: exampleutil.Must(rel.NewFilter("document"))})
	if e != nil || count.RelationshipCount != 42 || count.Revision.String() != "revision" {
		log.Fatalf("native count failed: %+v %v", count, e)
	}
	roaring, e := c.RoaringLookupResources(ctx, client.RoaringLookupResourcesRequest{ResourceType: "document", Permission: "view", Subject: exampleutil.Subject(exampleutil.Object("user", "alice"), ""), Consistency: consistency.Full()})
	if e != nil || roaring.Cardinality != 42 || len(roaring.Bitmap) != 3 || roaring.Revision.String() != "revision" {
		log.Fatalf("native roaring result failed: %+v %v", roaring, e)
	}
	events := 0
	for event, e := range c.WatchPermissionSets(ctx, client.WatchPermissionSetsRequest{}) {
		if e != nil || event.Kind != client.PermissionSetEventCompletedRevision || event.Revision.String() != "revision" {
			log.Fatalf("native materialize watch failed: %+v %v", event, e)
		}
		events++
		break
	}
	if events != 1 {
		log.Fatal("watch produced no event")
	}
	select {
	case <-stub.closed:
	case <-time.After(time.Second):
		log.Fatal("abandoned materialize stream was not canceled")
	}
	items := 0
	for item, e := range c.LookupPermissionSets(ctx, client.LookupPermissionSetsRequest{Limit: 5}) {
		if e != nil || !item.Cursor.Complete() || item.Cursor.Token() != "opaque" || item.Change.ChildMember.Object.ID() != "alice" {
			log.Fatalf("native materialize lookup failed: %+v %v", item, e)
		}
		items++
	}
	if items != 1 {
		log.Fatal("lookup produced no item")
	}
	download, e := c.DownloadPermissionSets(ctx, client.DownloadPermissionSetsRequest{}, client.WithDownloadPermissionSetsRevision(consistency.NewZedToken("revision")))
	if e != nil || len(download.Files) != 1 || download.Files[0].Name != "sets.avro" || download.Timestamp == nil || download.Revision.String() != "revision" {
		log.Fatalf("native materialize download metadata failed: %+v %v", download, e)
	}
	fmt.Println("materialize: all native operations and stream cancellation verified")
}
