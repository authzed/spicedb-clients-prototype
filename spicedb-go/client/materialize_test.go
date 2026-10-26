package client

import (
	"context"
	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"net"
	"testing"
	"time"
)

type materializeServer struct {
	materialize.UnimplementedRelationshipsServiceServer
	materialize.UnimplementedWatchPermissionSetsServiceServer
	t                         *testing.T
	watchClosed, lookupClosed chan struct{}
	lookupCursor              *materialize.Cursor
}

func (s *materializeServer) ExperimentalCountRelationshipsByFilter(ctx context.Context, r *materialize.ExperimentalCountRelationshipsByFilterRequest) (*materialize.ExperimentalCountRelationshipsByFilterResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	require.Equal(s.t, []string{"Bearer test-token"}, md.Get("authorization"))
	require.NotNil(s.t, r.RelationshipFilter.OptionalSubjectFilter.OptionalRelation)
	return &materialize.ExperimentalCountRelationshipsByFilterResponse{RelationshipCount: 12, ReadAt: &v1.ZedToken{Token: "count"}}, nil
}
func (s *materializeServer) WatchPermissionSets(r *materialize.WatchPermissionSetsRequest, stream materialize.WatchPermissionSetsService_WatchPermissionSetsServer) error {
	require.Equal(s.t, "start", r.OptionalStartingAfter.Token)
	_, hasDeadline := stream.Context().Deadline()
	require.False(s.t, hasDeadline)
	if e := stream.Send(&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_Change{Change: setChangeFixture()}}); e != nil {
		return e
	}
	<-stream.Context().Done()
	close(s.watchClosed)
	return stream.Context().Err()
}
func (s *materializeServer) LookupPermissionSets(r *materialize.LookupPermissionSetsRequest, stream materialize.WatchPermissionSetsService_LookupPermissionSetsServer) error {
	require.Equal(s.t, uint32(5), r.Limit)
	require.Equal(s.t, "revision", r.OptionalAtRevision.Token)
	require.True(s.t, proto.Equal(s.lookupCursor, r.OptionalStartingAfterCursor))
	if e := stream.Send(&materialize.LookupPermissionSetsResponse{Change: setChangeFixture(), Cursor: &materialize.Cursor{Limit: 5, Token: &v1.ZedToken{Token: "cursor-revision"}, StartingIndex: 12, StartingKey: "key", CompletedMembers: true, Cursor: "opaque"}}); e != nil {
		return e
	}
	<-stream.Context().Done()
	close(s.lookupClosed)
	return stream.Context().Err()
}
func (s *materializeServer) DownloadPermissionSets(ctx context.Context, r *materialize.DownloadPermissionSetsRequest) (*materialize.DownloadPermissionSetsResponse, error) {
	require.Equal(s.t, "revision", r.OptionalAtRevision.Token)
	_, hasDeadline := ctx.Deadline()
	require.True(s.t, hasDeadline)
	return &materialize.DownloadPermissionSetsResponse{Files: []*materialize.File{{Name: "sets.avro", Url: "https://download.invalid/file"}}, Timestamp: timestamppb.New(time.Unix(123, 0)), AtRevision: &v1.ZedToken{Token: "download"}}, nil
}
func setChangeFixture() *materialize.PermissionSetChange {
	return &materialize.PermissionSetChange{AtRevision: &v1.ZedToken{Token: "change"}, Operation: materialize.PermissionSetChange_SET_OPERATION_ADDED, ParentSet: &materialize.SetReference{ObjectType: "document", ObjectId: "d", PermissionOrRelation: "view"}, Child: &materialize.PermissionSetChange_ChildMember{ChildMember: &materialize.MemberReference{ObjectType: "user", ObjectId: "alice"}}}
}
func TestMaterializeNativeCoverageAndStreamRelease(t *testing.T) {
	s := &materializeServer{t: t, watchClosed: make(chan struct{}), lookupClosed: make(chan struct{}), lookupCursor: &materialize.Cursor{Limit: 5, StartingIndex: 3, Token: &v1.ZedToken{Token: "revision"}, StartingKey: "before", Cursor: "previous"}}
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	materialize.RegisterRelationshipsServiceServer(srv, s)
	materialize.RegisterWatchPermissionSetsServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	c := newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	ctx := context.Background()
	r, e := c.CountRelationshipsByFilter(ctx, CountRelationshipsByFilterRequest{Filter: testTriple("document", "d", "viewer", "user", "alice", "").Filter()})
	require.NoError(t, e)
	require.Equal(t, uint64(12), r.RelationshipCount)
	require.EqualValues(t, "count", r.Revision.String())
	for event, e := range c.WatchPermissionSets(ctx, WatchPermissionSetsRequest{}, WithWatchPermissionSetsStart(consistency.NewZedToken("start"))) {
		require.NoError(t, e)
		require.Equal(t, PermissionSetEventChange, event.Kind)
		require.Equal(t, PermissionSetAdded, event.Change.Operation)
		require.Equal(t, "alice", event.Change.ChildMember.Object.ID())
		break
	}
	select {
	case <-s.watchClosed:
	case <-time.After(time.Second):
		t.Fatal("watch permission sets leaked transport")
	}
	for result, e := range c.LookupPermissionSets(ctx, LookupPermissionSetsRequest{Limit: 5}, WithLookupPermissionSetsRevision(consistency.NewZedToken("revision")), WithLookupPermissionSetsCursor(nativePermissionSetCursor(s.lookupCursor))) {
		require.NoError(t, e)
		require.Equal(t, PermissionSetChildMember, result.Change.ChildKind)
		require.True(t, result.Cursor.Complete())
		require.Equal(t, "opaque", result.Cursor.Token())
		require.EqualValues(t, "cursor-revision", result.Cursor.Revision().String())
		break
	}
	select {
	case <-s.lookupClosed:
	case <-time.After(time.Second):
		t.Fatal("lookup permission sets leaked transport")
	}
	download, e := c.DownloadPermissionSets(ctx, DownloadPermissionSetsRequest{}, WithDownloadPermissionSetsRevision(consistency.NewZedToken("revision")))
	require.NoError(t, e)
	require.Equal(t, []PermissionSetFile{{Name: "sets.avro", URL: "https://download.invalid/file"}}, download.Files)
	require.EqualValues(t, "download", download.Revision.String())
	require.Equal(t, time.Unix(123, 0).UTC(), *download.Timestamp)
}
func TestPermissionSetVariantsAndUnknowns(t *testing.T) {
	p := setChangeFixture()
	p.Operation = 999
	r := nativePermissionSetChange(p)
	require.Equal(t, PermissionSetOperationUnspecified, r.Operation)
	p.Child = &materialize.PermissionSetChange_ChildSet{ChildSet: &materialize.SetReference{ObjectType: "group", ObjectId: "eng", PermissionOrRelation: "member"}}
	r = nativePermissionSetChange(p)
	require.Equal(t, PermissionSetChildSet, r.ChildKind)
	require.Nil(t, r.ChildMember)
	require.Equal(t, "member", r.ChildSet.PermissionOrRelation)
	p.Child = nil
	require.Equal(t, PermissionSetChildUnknown, nativePermissionSetChange(p).ChildKind)
	cases := []struct {
		p *materialize.WatchPermissionSetsResponse
		k PermissionSetEventKind
		r string
	}{{&materialize.WatchPermissionSetsResponse{}, PermissionSetEventUnknown, ""}, {&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_CompletedRevision{CompletedRevision: &v1.ZedToken{Token: "complete"}}}, PermissionSetEventCompletedRevision, "complete"}, {&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_LookupPermissionSetsRequired{LookupPermissionSetsRequired: &materialize.LookupPermissionSetsRequired{RequiredLookupAt: &v1.ZedToken{Token: "backfill"}}}}, PermissionSetEventLookupRequired, "backfill"}, {&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_BreakingSchemaChange{BreakingSchemaChange: &materialize.BreakingSchemaChange{ChangeAt: &v1.ZedToken{Token: "breaking"}, AffectedPermissions: []*materialize.WatchedPermission{{ResourceType: "document", Permission: "view", SubjectType: "group", OptionalSubjectRelation: "member"}}}}}, PermissionSetEventBreakingSchemaChange, "breaking"}}
	for _, tc := range cases {
		event := nativePermissionSetEvent(tc.p)
		require.Equal(t, tc.k, event.Kind)
		require.EqualValues(t, tc.r, event.Revision.String())
		if tc.k == PermissionSetEventBreakingSchemaChange {
			require.Equal(t, "member", event.AffectedPermissions[0].SubjectRelation)
		}
	}
}
