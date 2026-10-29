package client

import (
	"context"
	materialize "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/materialize/v0"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
	"net"
	"testing"
	"time"
)

type coverageServer struct {
	v1.UnimplementedSchemaServiceServer
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedExperimentalServiceServer
	materialize.UnimplementedRoaringLookupResourcesServiceServer
	t        *testing.T
	calls    map[string]int
	metadata map[string]any
}

func (s *coverageServer) called(ctx context.Context, name string) {
	s.calls[name]++
	_, ok := ctx.Deadline()
	require.True(s.t, ok, "unary %s must carry default deadline", name)
}
func (s *coverageServer) ReadSchema(ctx context.Context, _ *v1.ReadSchemaRequest) (*v1.ReadSchemaResponse, error) {
	s.called(ctx, "ReadSchema")
	return &v1.ReadSchemaResponse{SchemaText: "schema", ReadAt: &v1.ZedToken{Token: "read"}}, nil
}
func (s *coverageServer) WriteSchema(ctx context.Context, r *v1.WriteSchemaRequest) (*v1.WriteSchemaResponse, error) {
	s.called(ctx, "WriteSchema")
	require.Equal(s.t, "schema", r.Schema)
	return &v1.WriteSchemaResponse{WrittenAt: &v1.ZedToken{Token: "written"}}, nil
}
func (s *coverageServer) ReflectSchema(ctx context.Context, r *v1.ReflectSchemaRequest) (*v1.ReflectSchemaResponse, error) {
	s.called(ctx, "ReflectSchema")
	require.True(s.t, r.Consistency.GetFullyConsistent())
	require.Equal(s.t, "document", r.OptionalFilters[0].OptionalDefinitionNameFilter)
	return &v1.ReflectSchemaResponse{Definitions: []*v1.ReflectionDefinition{{Name: "document", Relations: []*v1.ReflectionRelation{{Name: "viewer", ParentDefinitionName: "document", SubjectTypes: []*v1.ReflectionTypeReference{{SubjectDefinitionName: "user", OptionalCaveatName: "c", Typeref: &v1.ReflectionTypeReference_IsTerminalSubject{IsTerminalSubject: true}}}}}}}, ReadAt: &v1.ZedToken{Token: "reflection"}}, nil
}
func (s *coverageServer) ComputablePermissions(ctx context.Context, r *v1.ComputablePermissionsRequest) (*v1.ComputablePermissionsResponse, error) {
	s.called(ctx, "ComputablePermissions")
	require.Equal(s.t, "document", r.DefinitionName)
	require.Equal(s.t, "viewer", r.RelationName)
	require.Equal(s.t, "filter", r.OptionalDefinitionNameFilter)
	return &v1.ComputablePermissionsResponse{Permissions: []*v1.ReflectionRelationReference{{DefinitionName: "document", RelationName: "view", IsPermission: true}}, ReadAt: &v1.ZedToken{Token: "computable"}}, nil
}
func (s *coverageServer) DependentRelations(ctx context.Context, r *v1.DependentRelationsRequest) (*v1.DependentRelationsResponse, error) {
	s.called(ctx, "DependentRelations")
	require.Equal(s.t, "view", r.PermissionName)
	return &v1.DependentRelationsResponse{Relations: []*v1.ReflectionRelationReference{{DefinitionName: "document", RelationName: "viewer"}}, ReadAt: &v1.ZedToken{Token: "dependencies"}}, nil
}
func (s *coverageServer) DiffSchema(ctx context.Context, r *v1.DiffSchemaRequest) (*v1.DiffSchemaResponse, error) {
	s.called(ctx, "DiffSchema")
	require.Equal(s.t, "comparison", r.ComparisonSchema)
	return &v1.DiffSchemaResponse{Diffs: []*v1.ReflectionSchemaDiff{{Diff: &v1.ReflectionSchemaDiff_CaveatParameterTypeChanged{CaveatParameterTypeChanged: &v1.ReflectionCaveatParameterTypeChange{Parameter: &v1.ReflectionCaveatParameter{Name: "ip", Type: "ipaddress", ParentCaveatName: "c"}, PreviousType: "string"}}}}, ReadAt: &v1.ZedToken{Token: "diff"}}, nil
}
func (s *coverageServer) ExpandPermissionTree(ctx context.Context, r *v1.ExpandPermissionTreeRequest) (*v1.ExpandPermissionTreeResponse, error) {
	s.called(ctx, "ExpandPermissionTree")
	require.Equal(s.t, "doc", r.Resource.ObjectId)
	return &v1.ExpandPermissionTreeResponse{ExpandedAt: &v1.ZedToken{Token: "expanded"}, TreeRoot: &v1.PermissionRelationshipTree{TreeType: &v1.PermissionRelationshipTree_Leaf{Leaf: &v1.DirectSubjectSet{Subjects: []*v1.SubjectReference{{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}}}}}}}, nil
}
func (s *coverageServer) WriteRelationships(ctx context.Context, r *v1.WriteRelationshipsRequest) (*v1.WriteRelationshipsResponse, error) {
	s.called(ctx, "Write")
	s.metadata = r.OptionalTransactionMetadata.AsMap()
	require.Len(s.t, r.Updates, 1)
	return nil, status.Error(codes.Unavailable, "ambiguous write")
}
func (s *coverageServer) ExperimentalRegisterRelationshipCounter(ctx context.Context, r *v1.ExperimentalRegisterRelationshipCounterRequest) (*v1.ExperimentalRegisterRelationshipCounterResponse, error) {
	s.called(ctx, "RegisterRelationshipCounter")
	require.Equal(s.t, "counter", r.Name)
	return &v1.ExperimentalRegisterRelationshipCounterResponse{}, nil
}
func (s *coverageServer) ExperimentalCountRelationships(ctx context.Context, r *v1.ExperimentalCountRelationshipsRequest) (*v1.ExperimentalCountRelationshipsResponse, error) {
	s.called(ctx, "CountRelationships")
	if s.calls["CountRelationships"] == 1 {
		return &v1.ExperimentalCountRelationshipsResponse{CounterResult: &v1.ExperimentalCountRelationshipsResponse_CounterStillCalculating{CounterStillCalculating: true}}, nil
	}
	return &v1.ExperimentalCountRelationshipsResponse{CounterResult: &v1.ExperimentalCountRelationshipsResponse_ReadCounterValue{ReadCounterValue: &v1.ReadCounterValue{RelationshipCount: 42, ReadAt: &v1.ZedToken{Token: "count"}}}}, nil
}
func (s *coverageServer) ExperimentalUnregisterRelationshipCounter(ctx context.Context, r *v1.ExperimentalUnregisterRelationshipCounterRequest) (*v1.ExperimentalUnregisterRelationshipCounterResponse, error) {
	s.called(ctx, "UnregisterRelationshipCounter")
	return &v1.ExperimentalUnregisterRelationshipCounterResponse{}, nil
}
func (s *coverageServer) ExperimentalRoaringLookupResources(ctx context.Context, r *materialize.ExperimentalRoaringLookupResourcesRequest) (*materialize.ExperimentalRoaringLookupResourcesResponse, error) {
	s.called(ctx, "RoaringLookupResources")
	require.Equal(s.t, "document", r.ResourceObjectType)
	require.Equal(s.t, "member", r.Subject.OptionalRelation)
	return &materialize.ExperimentalRoaringLookupResourcesResponse{Bitmap: []byte{1, 2, 3}, Cardinality: 42, AtRevision: &v1.ZedToken{Token: "roaring"}}, nil
}
func TestNativeUnaryRPCCoverageAndResults(t *testing.T) {
	s := &coverageServer{t: t, calls: map[string]int{}}
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterSchemaServiceServer(srv, s)
	v1.RegisterPermissionsServiceServer(srv, s)
	v1.RegisterExperimentalServiceServer(srv, s)
	materialize.RegisterRoaringLookupResourcesServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	c := newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	ctx := context.Background()
	read, e := c.ReadSchema(ctx, ReadSchemaRequest{})
	require.NoError(t, e)
	require.Equal(t, "schema", read.Schema)
	require.EqualValues(t, "read", read.Revision.String())
	write, e := c.WriteSchema(ctx, WriteSchemaRequest{Schema: "schema"})
	require.NoError(t, e)
	require.EqualValues(t, "written", write.Revision.String())
	filters := []SchemaFilter{{DefinitionName: "document"}}
	opt := WithReflectSchemaFilters(filters)
	filters[0].DefinitionName = "changed"
	reflection, e := c.ReflectSchema(ctx, ReflectSchemaRequest{Consistency: consistency.Full()}, opt)
	require.NoError(t, e)
	require.EqualValues(t, "reflection", reflection.Revision.String())
	subject := reflection.Definitions[0].Relations[0].SubjectTypes[0]
	require.Equal(t, SchemaTypeTerminal, subject.Kind)
	require.True(t, subject.Enabled)
	require.Equal(t, "c", subject.CaveatName)
	perms, e := c.ComputablePermissions(ctx, ComputablePermissionsRequest{DefinitionName: "document", RelationName: "viewer", Consistency: consistency.Full()}, WithComputablePermissionsDefinitionFilter("filter"))
	require.NoError(t, e)
	require.True(t, perms.Permissions[0].IsPermission)
	require.EqualValues(t, "computable", perms.Revision.String())
	deps, e := c.DependentRelations(ctx, DependentRelationsRequest{DefinitionName: "document", PermissionName: "view", Consistency: consistency.Full()})
	require.NoError(t, e)
	require.Equal(t, "viewer", deps.Relations[0].RelationName)
	require.EqualValues(t, "dependencies", deps.Revision.String())
	diff, e := c.DiffSchema(ctx, DiffSchemaRequest{ComparisonSchema: "comparison", Consistency: consistency.Full()})
	require.NoError(t, e)
	require.Equal(t, "caveat_parameter_type_changed", diff.Diffs[0].Kind)
	require.Equal(t, "ipaddress", diff.Diffs[0].Parameter.Type)
	require.Equal(t, "string", diff.Diffs[0].PreviousType)
	require.EqualValues(t, "diff", diff.Revision.String())
	tree, e := c.ExpandPermissionTree(ctx, ExpandPermissionTreeRequest{Resource: testObject("document", "doc"), Permission: "view", Consistency: consistency.Full()})
	require.NoError(t, e)
	require.Equal(t, "alice", tree.Tree.Subjects()[0].Object().ID())
	require.EqualValues(t, "expanded", tree.Revision.String())
	var tx rel.Txn
	require.NoError(t, tx.Touch(testTriple("document", "doc", "viewer", "user", "alice", "")))
	metadata := map[string]any{"nested": map[string]any{"null": nil}}
	_, e = c.Write(ctx, WriteRequest{Transaction: tx}, WithWriteTransactionMetadata(metadata))
	require.ErrorIs(t, e, ErrUnavailable)
	require.Equal(t, metadata, s.metadata)
	require.Equal(t, 1, s.calls["Write"])
	_, e = c.RegisterRelationshipCounter(ctx, RegisterRelationshipCounterRequest{Name: "counter", Filter: testFilter("document")})
	require.NoError(t, e)
	count, e := c.CountRelationships(ctx, CountRelationshipsRequest{Name: "counter"})
	require.NoError(t, e)
	require.True(t, count.StillCalculating)
	count, e = c.CountRelationships(ctx, CountRelationshipsRequest{Name: "counter"})
	require.NoError(t, e)
	require.False(t, count.StillCalculating)
	require.Equal(t, uint64(42), count.RelationshipCount)
	require.EqualValues(t, "count", count.Revision.String())
	_, e = c.UnregisterRelationshipCounter(ctx, UnregisterRelationshipCounterRequest{Name: "counter"})
	require.NoError(t, e)
	roaring, e := c.RoaringLookupResources(ctx, RoaringLookupResourcesRequest{ResourceType: "document", Permission: "view", Subject: testSubject("group", "eng", "member"), Consistency: consistency.Full()})
	require.NoError(t, e)
	require.Equal(t, []byte{1, 2, 3}, roaring.Bitmap)
	require.Equal(t, uint64(42), roaring.Cardinality)
	require.EqualValues(t, "roaring", roaring.Revision.String())
	require.Len(t, s.calls, 12)
}
func TestJSONSnapshotRejectsLossAndPreservesTypedContainers(t *testing.T) {
	r := testRequest("a")
	r.Context = map[string]any{"typed": map[string][]int{"list": {1, 2}}, "nullMap": map[string]any(nil), "nullList": []any(nil)}
	item, e := checkItem(r, nil)
	require.NoError(t, e)
	require.Equal(t, map[string]any{"typed": map[string]any{"list": []any{float64(1), float64(2)}}, "nullMap": nil, "nullList": nil}, item.Context.AsMap())
	for _, value := range []any{int64(1<<53 + 1), uint64(1 << 63)} {
		_, e = contextStruct(map[string]any{"large": value})
		require.ErrorIs(t, e, ErrInvalidArgument)
		require.Contains(t, e.Error(), "large")
	}
	ctx, _ := structpb.NewStruct(map[string]any{"null": nil})
	r2, e := nativeRelationship("test", &v1.Relationship{Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "d"}, Relation: "viewer", Subject: &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "a"}}, OptionalCaveat: &v1.ContextualizedCaveat{CaveatName: "c", Context: ctx}})
	require.NoError(t, e)
	require.Contains(t, r2.CaveatContext(), "null")
}

func TestRelationshipConversionPreservesEveryPersistedField(t *testing.T) {
	r := testTriple("document", "doc", "viewer", "group", "eng", "member")
	ctx := map[string]any{"string": "value", "number": 42, "bool": true, "null": nil, "map": map[string]any{"nested": true}, "list": []any{nil, "value", false}}
	r, e := r.WithCaveat("c", ctx)
	require.NoError(t, e)
	r = r.WithExpiration(time.Unix(123, 456).UTC())
	wire, e := relationship(r)
	require.NoError(t, e)
	back, e := nativeRelationship("test", wire)
	require.NoError(t, e)
	require.Equal(t, r.String(), back.String())
	require.Equal(t, r.CaveatName(), back.CaveatName())
	require.Equal(t, r.CaveatContext(), back.CaveatContext())
	require.Equal(t, r.Expiration(), back.Expiration())
	f := testFilter("document").WithResourceIDPrefix("prefix").WithSubjectType("group")
	named, e := rel.NamedRelation("member")
	require.NoError(t, e)
	f = f.WithSubjectRelation(named)
	protoFilter, e := filter(f)
	require.NoError(t, e)
	require.Equal(t, "prefix", protoFilter.OptionalResourceIdPrefix)
	require.Equal(t, "member", protoFilter.OptionalSubjectFilter.OptionalRelation.Relation)
	for _, op := range []v1.RelationshipUpdate_Operation{v1.RelationshipUpdate_OPERATION_UNSPECIFIED, v1.RelationshipUpdate_Operation(999)} {
		update, e := nativeUpdate("test", &v1.RelationshipUpdate{Operation: op, Relationship: wire})
		require.NoError(t, e)
		require.Equal(t, rel.UpdateOperationUnspecified, update.Operation)
	}
	check := checkResultFromProto(&v1.CheckPermissionResponse{Permissionship: 999})
	require.Equal(t, PermissionshipUnspecified, check.Permissionship)
	require.False(t, check.HasPermission())
}
