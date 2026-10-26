package client

import (
	"context"
	"slices"
	"testing"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func traceFixture() *v1.DebugInformation {
	ctx, _ := structpb.NewStruct(map[string]any{"null": nil, "nested": []any{true, 3, "x"}})
	return &v1.DebugInformation{SchemaUsed: "definition user {}", Check: &v1.CheckDebugTrace{
		Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "a"}, Permission: "view", PermissionType: v1.CheckDebugTrace_PERMISSION_TYPE_PERMISSION,
		Subject: &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "group", ObjectId: "eng"}, OptionalRelation: "member"}, Result: v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
		Duration: durationpb.New(123 * time.Millisecond), OptionalExpiresAt: timestamppb.New(time.Unix(123, 456).UTC()), TraceOperationId: "opaque", Source: "spicedb:node",
		CaveatEvaluationInfo: &v1.CaveatEvalInfo{Expression: "x == 3", Result: v1.CaveatEvalInfo_RESULT_MISSING_SOME_CONTEXT, Context: ctx, PartialCaveatInfo: &v1.PartialCaveatInfo{MissingRequiredContext: []string{"x"}}, CaveatName: "c"},
		Resolution:           &v1.CheckDebugTrace_SubProblems_{SubProblems: &v1.CheckDebugTrace_SubProblems{Traces: []*v1.CheckDebugTrace{{Permission: "viewer", PermissionType: v1.CheckDebugTrace_PERMISSION_TYPE_RELATION, Result: v1.CheckDebugTrace_PERMISSIONSHIP_HAS_PERMISSION, Resolution: &v1.CheckDebugTrace_WasCachedResult{WasCachedResult: false}}}}},
	}}
}
func TestCheckTracingSingularWireAndRecursiveNativeConversion(t *testing.T) {
	c := checkClient(t, &checkServer{single: func(_ context.Context, r *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
		require.True(t, r.WithTracing)
		return &v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, DebugTrace: traceFixture()}, nil
	}})
	r, e := c.Check(context.Background(), testRequest("a"), WithCheckTracing(TracingEnabled))
	require.NoError(t, e)
	require.True(t, r.HasPermission())
	require.Equal(t, "definition user {}", r.DebugTrace.SchemaUsed)
	tr := r.DebugTrace.Check
	require.Equal(t, "document", tr.Resource.Type())
	require.Equal(t, "a", tr.Resource.ID())
	require.Equal(t, "view", tr.Permission)
	require.Equal(t, TracePermission, tr.PermissionType)
	require.Equal(t, "eng", tr.Subject.Object().ID())
	require.Equal(t, "member", tr.Subject.Relation())
	require.Equal(t, PermissionshipConditionalPermission, tr.Result)
	require.Equal(t, 123*time.Millisecond, *tr.Duration)
	require.Equal(t, time.Unix(123, 456).UTC(), *tr.Expiration)
	require.Equal(t, "opaque", tr.TraceOperationID)
	require.Equal(t, "spicedb:node", tr.Source)
	require.Equal(t, TraceSubproblems, tr.Resolution)
	require.Nil(t, tr.WasCachedResult)
	require.Len(t, tr.Subproblems, 1)
	child := tr.Subproblems[0]
	require.Equal(t, TraceRelation, child.PermissionType)
	require.Equal(t, TraceCached, child.Resolution)
	require.NotNil(t, child.WasCachedResult)
	require.False(t, *child.WasCachedResult)
	require.Nil(t, child.Duration)
	require.Nil(t, child.Expiration)
	require.Equal(t, "x == 3", tr.CaveatEvaluation.Expression)
	require.Equal(t, CaveatMissingContext, tr.CaveatEvaluation.Result)
	require.Equal(t, []string{"x"}, tr.CaveatEvaluation.MissingContext)
	require.Equal(t, "c", tr.CaveatEvaluation.CaveatName)
	require.Contains(t, tr.CaveatEvaluation.Context, "null")
	require.Nil(t, tr.CaveatEvaluation.Context["null"])
	require.Equal(t, []any{true, float64(3), "x"}, tr.CaveatEvaluation.Context["nested"])
}
func TestCheckTracingPerBulkItemAndEveryChunk(t *testing.T) {
	calls := 0
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		calls++
		require.True(t, r.WithTracing)
		response := successfulBulk(r)
		response.Pairs[0].GetItem().DebugTrace = traceFixture()
		return response, nil
	}})
	rs := make([]PermissionCheck, 1001)
	for i := range rs {
		rs[i] = testRequest("a")
	}
	out, e := c.CheckBulk(context.Background(), rs, WithCheckTracing(TracingEnabled))
	require.NoError(t, e)
	require.NotNil(t, out[0].DebugTrace)
	require.Nil(t, out[1].DebugTrace)
	require.NotNil(t, out[1000].DebugTrace)
	require.Equal(t, 2, calls)
	count := 0
	for _, e := range c.CheckIter(context.Background(), slices.Values(rs), WithCheckTracing(TracingEnabled)) {
		require.NoError(t, e)
		count++
	}
	require.Equal(t, 1001, count)
	require.Equal(t, 4, calls)
}
func TestTraceUnknownEnumsAndAbsentPresence(t *testing.T) {
	require.Nil(t, debugInformation(nil))
	p := traceFixture()
	p.Check.PermissionType = 999
	p.Check.Result = 999
	p.Check.CaveatEvaluationInfo.Result = 999
	p.Check.Resolution = nil
	r := debugInformation(p)
	require.Equal(t, TracePermissionUnspecified, r.Check.PermissionType)
	require.Equal(t, PermissionshipUnspecified, r.Check.Result)
	require.Equal(t, CaveatEvaluationUnspecified, r.Check.CaveatEvaluation.Result)
	require.Equal(t, TraceResolutionUnspecified, r.Check.Resolution)
	require.Nil(t, r.Check.WasCachedResult)
	require.Nil(t, r.Check.Subproblems)
	for _, v := range []v1.CheckDebugTrace_Permissionship{v1.CheckDebugTrace_PERMISSIONSHIP_NO_PERMISSION, v1.CheckDebugTrace_PERMISSIONSHIP_HAS_PERMISSION, v1.CheckDebugTrace_PERMISSIONSHIP_CONDITIONAL_PERMISSION} {
		p.Check.Result = v
		require.NotEqual(t, PermissionshipUnspecified, debugInformation(p).Check.Result)
	}
}

func TestSingularCheckExpirationIsIndependentOfTrace(t *testing.T) {
	expiry := time.Unix(100, 123).UTC()
	result := checkResultFromProto(&v1.CheckPermissionResponse{OptionalExpiresAt: timestamppb.New(expiry)})
	require.NotNil(t, result.Expiration)
	require.Equal(t, expiry, *result.Expiration)
	require.Nil(t, result.DebugTrace)
	require.Nil(t, checkResultFromProto(&v1.CheckPermissionResponse{}).Expiration)
	require.Nil(t, checkResultFromBulkItem(&v1.CheckBulkPermissionsResponseItem{}, "").Expiration)
}
