package client

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// malformedServer answers every read with one valid item followed by the
// configured malformed one, and counts calls so a test can prove the client
// did not retry.
type malformedServer struct {
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedWatchServiceServer
	calls atomic.Int32

	read    *v1.ReadRelationshipsResponse
	export  *v1.ExportBulkRelationshipsResponse
	lookup  *v1.LookupResourcesResponse
	subject *v1.LookupSubjectsResponse
	watch   *v1.WatchResponse
	expand  *v1.ExpandPermissionTreeResponse
}

func validWire() *v1.Relationship {
	p, _ := relationship(testTriple("document", "ok", "viewer", "user", "alice", ""))
	return p
}

func (s *malformedServer) ReadRelationships(_ *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	s.calls.Add(1)
	if e := stream.Send(&v1.ReadRelationshipsResponse{Relationship: validWire(), AfterResultCursor: &v1.Cursor{Token: "1"}}); e != nil {
		return e
	}
	return stream.Send(s.read)
}

func (s *malformedServer) ExportBulkRelationships(_ *v1.ExportBulkRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ExportBulkRelationshipsResponse]) error {
	s.calls.Add(1)
	return stream.Send(s.export)
}

func (s *malformedServer) LookupResources(_ *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	s.calls.Add(1)
	if e := stream.Send(&v1.LookupResourcesResponse{ResourceObjectId: "ok", Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, AfterResultCursor: &v1.Cursor{Token: "1"}}); e != nil {
		return e
	}
	return stream.Send(s.lookup)
}

func (s *malformedServer) LookupSubjects(_ *v1.LookupSubjectsRequest, stream grpc.ServerStreamingServer[v1.LookupSubjectsResponse]) error {
	s.calls.Add(1)
	if e := stream.Send(&v1.LookupSubjectsResponse{Subject: &v1.ResolvedSubject{SubjectObjectId: "ok"}}); e != nil {
		return e
	}
	return stream.Send(s.subject)
}

func (s *malformedServer) Watch(_ *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	s.calls.Add(1)
	return stream.Send(s.watch)
}

func (s *malformedServer) ExpandPermissionTree(context.Context, *v1.ExpandPermissionTreeRequest) (*v1.ExpandPermissionTreeResponse, error) {
	s.calls.Add(1)
	return s.expand, nil
}

func newMalformedClient(t *testing.T, srv *malformedServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(gs, srv)
	v1.RegisterWatchServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	c, err := NewWithOpts("passthrough:///bufnet", "t", WithInsecure(), WithRemotePlaintextCredentials(RemotePlaintextAllowed),
		WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// requireMalformed asserts an error is the client's INTERNAL malformed-response
// error, which no default policy retries.
func requireMalformed(t *testing.T, err error, field string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, CodeInternal, CodeOf(err), "%v", err)
	require.ErrorIs(t, err, ErrInternal)
	require.ErrorContains(t, err, "malformed response")
	require.ErrorContains(t, err, field)
	require.False(t, DefaultRetryPolicy().retryable(err))
}

// A response missing a required field fails with INTERNAL: the item before
// it is yielded, the malformed one is neither skipped nor filled with empty
// values, and the read ends there without a retry.
func TestMalformedResponses_FailWithInternal(t *testing.T) {
	ctx := context.Background()
	missingSubject := validWire()
	missingSubject.Subject = nil
	missingResource := validWire()
	missingResource.Resource = &v1.ObjectReference{ObjectType: "document"}

	cases := []struct {
		name  string
		srv   *malformedServer
		run   func(c *Client) (int, error)
		items int
		field string
	}{
		{"read missing relationship", &malformedServer{read: &v1.ReadRelationshipsResponse{AfterResultCursor: &v1.Cursor{Token: "2"}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.ReadRelationships(ctx, ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}))
			return n, err
		}, 1, "relationship is missing"},
		{"read missing subject", &malformedServer{read: &v1.ReadRelationshipsResponse{Relationship: missingSubject, AfterResultCursor: &v1.Cursor{Token: "2"}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.ReadRelationships(ctx, ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}))
			return n, err
		}, 1, "malformed server relationship"},
		{"export missing resource id", &malformedServer{export: &v1.ExportBulkRelationshipsResponse{Relationships: []*v1.Relationship{validWire(), missingResource}, AfterResultCursor: &v1.Cursor{Token: "1"}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.ExportRelationships(ctx, ExportRelationshipsRequest{Consistency: consistency.Full()}))
			return n, err
		}, 1, "malformed server relationship"},
		{"lookup resources missing resource", &malformedServer{lookup: &v1.LookupResourcesResponse{Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, AfterResultCursor: &v1.Cursor{Token: "2"}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.LookupResources(ctx, lookupReq()))
			return n, err
		}, 1, "resource object id is missing"},
		{"lookup subjects missing subject", &malformedServer{subject: &v1.LookupSubjectsResponse{}}, func(c *Client) (int, error) {
			n, _, err := drain(c.LookupSubjects(ctx, LookupSubjectsRequest{Resource: testObject("document", "1"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()}))
			return n, err
		}, 1, "subject is missing"},
		{"lookup subjects missing excluded subject", &malformedServer{subject: &v1.LookupSubjectsResponse{Subject: &v1.ResolvedSubject{SubjectObjectId: "*"}, ExcludedSubjects: []*v1.ResolvedSubject{{}}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.LookupSubjects(ctx, LookupSubjectsRequest{Resource: testObject("document", "1"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()}))
			return n, err
		}, 1, "excluded subject id is missing"},
		{"watch update missing relationship", &malformedServer{watch: &v1.WatchResponse{Updates: []*v1.RelationshipUpdate{{Operation: v1.RelationshipUpdate_OPERATION_TOUCH}}}}, func(c *Client) (int, error) {
			n, _, err := drain(c.Updates(ctx, UpdatesRequest{}))
			return n, err
		}, 0, "update relationship is missing"},
		{"expand missing tree root", &malformedServer{expand: &v1.ExpandPermissionTreeResponse{}}, func(c *Client) (int, error) {
			_, err := c.ExpandPermissionTree(ctx, ExpandPermissionTreeRequest{Resource: testObject("document", "1"), Permission: "view", Consistency: consistency.Full()})
			return 0, err
		}, 0, "tree root is missing"},
		{"expand leaf subject missing object", &malformedServer{expand: &v1.ExpandPermissionTreeResponse{TreeRoot: &v1.PermissionRelationshipTree{TreeType: &v1.PermissionRelationshipTree_Leaf{Leaf: &v1.DirectSubjectSet{Subjects: []*v1.SubjectReference{{}}}}}}}, func(c *Client) (int, error) {
			_, err := c.ExpandPermissionTree(ctx, ExpandPermissionTreeRequest{Resource: testObject("document", "1"), Permission: "view", Consistency: consistency.Full()})
			return 0, err
		}, 0, "leaf subject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newMalformedClient(t, tc.srv)
			n, err := tc.run(c)
			requireMalformed(t, err, tc.field)
			require.Equal(t, tc.items, n)
			require.EqualValues(t, 1, tc.srv.calls.Load(), "a malformed response is never retried")
		})
	}
}

// Materialize changes missing their required parts fail the same way.
func TestMalformedResponses_PermissionSets(t *testing.T) {
	_, err := nativePermissionSetChange("lookup permission sets", nil)
	requireMalformed(t, err, "permission set change is missing")

	p := setChangeFixture()
	p.ParentSet = nil
	_, err = nativePermissionSetChange("lookup permission sets", p)
	requireMalformed(t, err, "parent set is missing")

	p = setChangeFixture()
	p.Child = &materialize.PermissionSetChange_ChildMember{ChildMember: &materialize.MemberReference{ObjectType: "user"}}
	_, err = nativePermissionSetChange("lookup permission sets", p)
	requireMalformed(t, err, "child member object")

	_, err = nativePermissionSetEvent(&materialize.WatchPermissionSetsResponse{Response: &materialize.WatchPermissionSetsResponse_Change{}})
	requireMalformed(t, err, "permission set change is missing")
}
