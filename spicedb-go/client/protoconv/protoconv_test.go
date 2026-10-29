package protoconv_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/protoconv"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func obj(t, id string) *v1.ObjectReference { return &v1.ObjectReference{ObjectType: t, ObjectId: id} }

var (
	checkResponse = &v1.CheckPermissionResponse{
		CheckedAt:         &v1.ZedToken{Token: "rev1"},
		Permissionship:    v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
		PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"cidr"}},
		OptionalExpiresAt: timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)),
		DebugTrace: &v1.DebugInformation{SchemaUsed: "definition user {}", Check: &v1.CheckDebugTrace{
			Resource: obj("document", "readme"), Permission: "view", PermissionType: v1.CheckDebugTrace_PERMISSION_TYPE_PERMISSION,
			Subject: &v1.SubjectReference{Object: obj("user", "alice")}, Result: v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
			Duration: durationpb.New(3 * time.Millisecond), TraceOperationId: "op", Source: "spicedb:dispatch",
			Resolution: &v1.CheckDebugTrace_SubProblems_{SubProblems: &v1.CheckDebugTrace_SubProblems{Traces: []*v1.CheckDebugTrace{{
				Resource: obj("document", "readme"), Permission: "viewer", PermissionType: v1.CheckDebugTrace_PERMISSION_TYPE_RELATION,
				Subject: &v1.SubjectReference{Object: obj("user", "alice")}, Result: v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
				Resolution: &v1.CheckDebugTrace_WasCachedResult{WasCachedResult: false},
				CaveatEvaluationInfo: &v1.CaveatEvalInfo{Expression: "cidr == x", Result: v1.CaveatEvalInfo_RESULT_MISSING_SOME_CONTEXT,
					Context: mustStruct(map[string]any{"a": 1.0}), PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"cidr"}}, CaveatName: "ip"},
			}}}},
		}},
	}
	expandResponse = &v1.ExpandPermissionTreeResponse{ExpandedAt: &v1.ZedToken{Token: "rev2"}, TreeRoot: &v1.PermissionRelationshipTree{
		ExpandedObject: obj("document", "readme"), ExpandedRelation: "view",
		TreeType: &v1.PermissionRelationshipTree_Intermediate{Intermediate: &v1.AlgebraicSubjectSet{
			Operation: v1.AlgebraicSubjectSet_OPERATION_UNION,
			Children: []*v1.PermissionRelationshipTree{{
				ExpandedObject: obj("document", "readme"), ExpandedRelation: "viewer",
				TreeType: &v1.PermissionRelationshipTree_Leaf{Leaf: &v1.DirectSubjectSet{Subjects: []*v1.SubjectReference{
					{Object: obj("user", "alice")}, {Object: obj("team", "eng"), OptionalRelation: "member"},
				}}},
			}},
		}},
	}}
	// As SpiceDB sends it: the deprecated top-level fields mirror Subject.
	lookupSubjectsResponse = &v1.LookupSubjectsResponse{
		LookedUpAt:      &v1.ZedToken{Token: "rev3"},
		Subject:         &v1.ResolvedSubject{SubjectObjectId: "*", Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION},
		SubjectObjectId: "*",
		Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
		ExcludedSubjects: []*v1.ResolvedSubject{{SubjectObjectId: "bob", Permissionship: v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
			PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"k"}}}},
		ExcludedSubjectIds: []string{"bob"},
	}
	readResponses = []*v1.ReadRelationshipsResponse{
		{ReadAt: &v1.ZedToken{Token: "rev4"}, AfterResultCursor: &v1.Cursor{Token: "c1"}, Relationship: &v1.Relationship{Resource: obj("document", "a"), Relation: "viewer", Subject: &v1.SubjectReference{Object: obj("user", "alice")},
			OptionalCaveat: &v1.ContextualizedCaveat{CaveatName: "ip", Context: mustStruct(map[string]any{"cidr": "x"})}}},
		{ReadAt: &v1.ZedToken{Token: "rev4"}, AfterResultCursor: &v1.Cursor{Token: "c2"}, Relationship: &v1.Relationship{Resource: obj("document", "b"), Relation: "viewer", Subject: &v1.SubjectReference{Object: obj("user", "bob")},
			OptionalExpiresAt: timestamppb.New(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))}},
	}
)

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(err)
	}
	return s
}

type server struct {
	v1.UnimplementedPermissionsServiceServer
	readCursor string
}

func (s *server) CheckPermission(ctx context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	_ = grpc.SetHeader(ctx, metadata.Pairs("io.spicedb.respmeta.version", "v1.99.0"))
	_ = grpc.SetTrailer(ctx, metadata.Pairs("trailer-key", "trailer-value"))
	return checkResponse, nil
}

func (s *server) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	resp := &v1.CheckBulkPermissionsResponse{CheckedAt: &v1.ZedToken{Token: "bulk-rev"}}
	for _, item := range req.GetItems() {
		pair := &v1.CheckBulkPermissionsPair{Request: item}
		if item.GetResource().GetObjectId() == "broken" {
			pair.Response = &v1.CheckBulkPermissionsPair_Error{Error: &rpcstatus.Status{Code: int32(codes.FailedPrecondition), Message: "permission view not found"}}
		} else {
			pair.Response = &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{
				Permissionship:    v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
				PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"cidr"}}}}
		}
		resp.Pairs = append(resp.Pairs, pair)
	}
	return resp, nil
}

func (s *server) ExpandPermissionTree(context.Context, *v1.ExpandPermissionTreeRequest) (*v1.ExpandPermissionTreeResponse, error) {
	return expandResponse, nil
}

func (s *server) LookupSubjects(_ *v1.LookupSubjectsRequest, stream grpc.ServerStreamingServer[v1.LookupSubjectsResponse]) error {
	return stream.Send(lookupSubjectsResponse)
}

func (s *server) ReadRelationships(req *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	s.readCursor = req.GetOptionalCursor().GetToken()
	for _, r := range readResponses {
		if err := stream.Send(r); err != nil {
			return err
		}
	}
	return nil
}

func newClient(t *testing.T) (*client.Client, *server, *grpc.ClientConn) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	srv := &server{}
	g := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(g, srv)
	go func() { _ = g.Serve(listener) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	c, err := client.NewFromConn(conn)
	require.NoError(t, err)
	return c, srv, conn
}

func check(t *testing.T) client.PermissionCheck {
	return client.PermissionCheck{
		Resource: rel.MustNewObject("document", "readme"), Permission: "view",
		Subject: rel.MustNewSubject(rel.MustNewObject("user", "alice"), ""), Consistency: consistency.Full(),
	}
}

func TestResultsRoundTripToTheServersMessages(t *testing.T) {
	c, _, _ := newClient(t)
	ctx := context.Background()

	result, err := c.Check(ctx, check(t))
	require.NoError(t, err)
	got, err := protoconv.CheckResultToProto(result)
	require.NoError(t, err)
	require.True(t, proto.Equal(checkResponse, got), "check:\nwant %v\ngot  %v", checkResponse, got)

	expanded, err := c.ExpandPermissionTree(ctx, client.ExpandPermissionTreeRequest{Resource: rel.MustNewObject("document", "readme"), Permission: "view", Consistency: consistency.Full()})
	require.NoError(t, err)
	require.True(t, proto.Equal(expandResponse, protoconv.ExpandResultToProto(expanded)))

	for subject, err := range c.LookupSubjects(ctx, client.LookupSubjectsRequest{Resource: rel.MustNewObject("document", "readme"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()}) {
		require.NoError(t, err)
		require.True(t, proto.Equal(lookupSubjectsResponse, protoconv.LookupSubjectToProto(subject)))
	}

	i := 0
	for r, err := range c.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("document"), Consistency: consistency.Full()}) {
		require.NoError(t, err)
		got, err := protoconv.ReadRelationshipToProto(r)
		require.NoError(t, err)
		require.True(t, proto.Equal(readResponses[i], got), "read %d", i)
		i++
	}
	require.Equal(t, 2, i)
}

func TestNewFromConnLeavesConnectionOpen(t *testing.T) {
	c, _, conn := newClient(t)
	require.NoError(t, c.Close())
	_, err := c.Check(context.Background(), check(t))
	require.NoError(t, err, "Close must not close a caller-owned connection")
	require.NotNil(t, conn)

	_, err = client.NewFromConn(conn, client.WithInsecure())
	require.ErrorIs(t, err, client.ErrInvalidArgument)
	_, err = client.NewFromConn(nil)
	require.ErrorIs(t, err, client.ErrInvalidArgument)
}

func TestResponseMetadataEscapeHatch(t *testing.T) {
	c, _, _ := newClient(t)
	var header, trailer metadata.MD
	_, err := c.Check(context.Background(), check(t), client.WithResponseMetadata[client.CheckOption](&header, &trailer))
	require.NoError(t, err)
	require.Equal(t, []string{"v1.99.0"}, header.Get("io.spicedb.respmeta.version"))
	require.Equal(t, []string{"trailer-value"}, trailer.Get("trailer-key"))
}

func TestReadExposesCursorsAndResumes(t *testing.T) {
	c, srv, _ := newClient(t)
	var cursors []string
	for r, err := range c.ReadRelationships(context.Background(), client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("document"), Consistency: consistency.Full()},
		client.WithReadRelationshipsCursor("resume-here")) {
		require.NoError(t, err)
		require.Equal(t, "rev4", r.ReadAt.String())
		cursors = append(cursors, r.AfterResultCursor)
	}
	require.Equal(t, []string{"c1", "c2"}, cursors)
	require.Equal(t, "resume-here", srv.readCursor)
}

func TestRelationshipLevelRoundTrips(t *testing.T) {
	r := rel.MustFromTuple(`document:readme#viewer@team:eng#member[ip:{"cidr":"x"}][expiration:2030-01-01T00:00:00Z]`)
	p, err := protoconv.RelationshipToProto(r)
	require.NoError(t, err)
	back, err := protoconv.RelationshipFromProto(p)
	require.NoError(t, err)
	require.Equal(t, r.String(), back.String())

	named, _ := rel.NamedRelation("member")
	for _, f := range []rel.Filter{
		rel.MustNewFilter("document"),
		rel.MustNewFilter("document").WithResourceIDPrefix("re").WithRelation("viewer").WithSubjectType("user").WithSubjectRelation(rel.DirectRelation()),
		rel.MustNewFilter("document").WithResourceID("readme").WithSubjectType("team").WithSubjectID("eng").WithSubjectRelation(named),
	} {
		p, err := protoconv.FilterToProto(f)
		require.NoError(t, err)
		back, err := protoconv.FilterFromProto(p)
		require.NoError(t, err)
		require.Equal(t, f.String(), back.String())
	}

	for _, s := range []consistency.Strategy{consistency.Full(), consistency.MinLatency(), consistency.AtLeast(consistency.NewZedToken("t")), consistency.Snapshot(consistency.NewZedToken("t"))} {
		p, err := protoconv.ConsistencyToProto(s)
		require.NoError(t, err)
		back, err := protoconv.ConsistencyFromProto(p)
		require.NoError(t, err)
		require.Equal(t, s, back)
	}

	u := rel.Update{Operation: rel.UpdateOperationDelete, Relationship: r}
	pu, err := protoconv.UpdateToProto(u)
	require.NoError(t, err)
	backU, err := protoconv.UpdateFromProto(pu)
	require.NoError(t, err)
	require.Equal(t, u.String(), backU.String())

	pre := rel.Precondition{Filter: rel.MustNewFilter("document"), Operation: rel.PreconditionMustMatch}
	pp, err := protoconv.PreconditionToProto(pre)
	require.NoError(t, err)
	backP, err := protoconv.PreconditionFromProto(pp)
	require.NoError(t, err)
	require.Equal(t, pre.String(), backP.String())
}

func TestCheckBulkRoundTripsTheServersResponse(t *testing.T) {
	c, _, _ := newClient(t)
	requests := []client.PermissionCheck{check(t), check(t), check(t)}
	requests[1].Resource = rel.MustNewObject("document", "broken")
	requests[2].Context = map[string]any{"cidr": "10.0.0.0/8"}
	results, err := c.CheckBulk(context.Background(), requests, client.WithBulkItemErrors(client.ItemErrorsReturned))
	require.NoError(t, err)

	got, err := protoconv.CheckBulkToProto(requests, results)
	require.NoError(t, err)
	want := &v1.CheckBulkPermissionsResponse{CheckedAt: &v1.ZedToken{Token: "bulk-rev"}}
	for _, r := range requests {
		item, err := protoconv.CheckRequestToProto(r)
		require.NoError(t, err)
		pair := &v1.CheckBulkPermissionsPair{Request: item}
		if r.Resource.ID() == "broken" {
			pair.Response = &v1.CheckBulkPermissionsPair_Error{Error: &rpcstatus.Status{Code: int32(codes.FailedPrecondition), Message: "permission view not found"}}
		} else {
			pair.Response = &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{
				Permissionship:    v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
				PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"cidr"}}}}
		}
		want.Pairs = append(want.Pairs, pair)
	}
	require.True(t, proto.Equal(want, got), "want %v\ngot  %v", want, got)

	_, err = protoconv.CheckBulkToProto(requests[:1], results)
	require.Error(t, err)
}
