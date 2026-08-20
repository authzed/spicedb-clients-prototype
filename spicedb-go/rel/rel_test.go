package rel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func TestFromTriple(t *testing.T) {
	r, err := rel.FromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Equal(t, "doc1", r.ResourceID)
	require.Equal(t, "viewer", r.ResourceRelation)
	require.Equal(t, "user", r.SubjectType)
	require.Equal(t, "alice", r.SubjectID)
	require.Empty(t, r.SubjectRelation)
}

func TestFromTripleValidation(t *testing.T) {
	_, err := rel.FromTriple("", "doc1", "viewer", "user", "alice", "")
	require.ErrorIs(t, err, rel.ErrInvalidResource)

	_, err = rel.FromTriple("document", "doc1", "viewer", "", "", "")
	require.ErrorIs(t, err, rel.ErrInvalidSubject)
}

func TestMustFromTriple(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.Equal(t, "document", r.ResourceType)

	require.Panics(t, func() {
		rel.MustFromTriple("", "", "", "", "", "")
	})
}

func TestFromTuple(t *testing.T) {
	r, err := rel.FromTuple("document:doc1#viewer@user:alice")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Equal(t, "doc1", r.ResourceID)
	require.Equal(t, "viewer", r.ResourceRelation)
	require.Equal(t, "user", r.SubjectType)
	require.Equal(t, "alice", r.SubjectID)
}

func TestFromTupleWithSubjectRelation(t *testing.T) {
	r, err := rel.FromTuple("document:doc1#viewer@group:eng#member")
	require.NoError(t, err)
	require.Equal(t, "member", r.SubjectRelation)
}

func TestFromTupleInvalid(t *testing.T) {
	_, err := rel.FromTuple("invalid")
	require.Error(t, err)
}

func TestFromObjects(t *testing.T) {
	r, err := rel.FromObjects("document", "doc1", "viewer", "user", "alice")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Empty(t, r.SubjectRelation)
}

func TestWithCaveat(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	r2 := r.WithCaveat("is_public", map[string]any{"public": true})
	require.Equal(t, "is_public", r2.CaveatName)
	require.Equal(t, map[string]any{"public": true}, r2.CaveatContext)
	require.Empty(t, r.CaveatName, "original should be unchanged")
}

func TestWithExpiration(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	exp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	r2 := r.WithExpiration(exp)
	require.NotNil(t, r2.Expiration)
	require.Equal(t, exp, *r2.Expiration)
	require.Nil(t, r.Expiration, "original should be unchanged")
}

func TestString(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.Equal(t, "document:doc1#viewer@user:alice", r.String())

	r2 := rel.MustFromTriple("document", "doc1", "viewer", "group", "eng", "member")
	require.Equal(t, "document:doc1#viewer@group:eng#member", r2.String())
}

func TestFilter(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	f := r.Filter()
	require.Equal(t, "document", f.ResourceType)
	require.Equal(t, "doc1", f.ResourceID)
	require.Equal(t, "viewer", f.Relation)
}

func TestNewFilter(t *testing.T) {
	f := rel.NewFilter("document").
		WithResourceID("doc1").
		WithRelation("viewer").
		WithSubjectType("user").
		WithSubjectID("alice")
	require.Equal(t, "document", f.ResourceType)
	require.Equal(t, "doc1", f.ResourceID)
	require.Equal(t, "viewer", f.Relation)
	require.Equal(t, "user", f.SubjectType)
	require.Equal(t, "alice", f.SubjectID)
}

func TestTxn(t *testing.T) {
	r1 := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "doc2", "editor", "user", "bob", "")

	var txn rel.Txn
	require.NoError(t, txn.Create(r1))
	require.NoError(t, txn.Touch(r2))
	require.NoError(t, txn.Delete(r1))
	require.NoError(t, txn.MustNotMatch(rel.NewFilter("document").WithResourceID("doc3")))

	require.Len(t, txn.V1Updates, 3)
	require.Len(t, txn.Preconditions(), 1)
}

func TestRoundTripProto(t *testing.T) {
	original := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("is_public", map[string]any{"public": true})

	proto, err := original.ToProto()
	require.NoError(t, err)
	roundTripped := rel.FromProto(proto)

	require.Equal(t, original.ResourceType, roundTripped.ResourceType)
	require.Equal(t, original.ResourceID, roundTripped.ResourceID)
	require.Equal(t, original.ResourceRelation, roundTripped.ResourceRelation)
	require.Equal(t, original.SubjectType, roundTripped.SubjectType)
	require.Equal(t, original.SubjectID, roundTripped.SubjectID)
	require.Equal(t, original.CaveatName, roundTripped.CaveatName)
}

// TestToProtoPreservesCaveatContextTypes asserts that ToProto dispatches
// each caveat context value onto the correct google.protobuf.Value kind
// (string_value, number_value, bool_value, null_value, struct_value,
// list_value) rather than stringifying it. Go's structpb.NewStruct already
// does this dispatch correctly — this test documents that the write path
// keeps it — see TestToProtoInvalidCaveatContextReturnsError for the actual
// pre-fix defect (a swallowed conversion error), which this test alone does
// not exercise.
func TestToProtoPreservesCaveatContextTypes(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("some_caveat", map[string]any{
			"a_string": "hello",
			"an_int":   42,
			"a_float":  3.5,
			"a_bool":   true,
			"a_null":   nil,
			"a_map":    map[string]any{"nested": "value"},
			"a_list":   []any{"one", 2, false},
		})

	proto, err := r.ToProto()
	require.NoError(t, err)

	fields := proto.GetOptionalCaveat().GetContext().GetFields()

	require.IsType(t, &structpb.Value_StringValue{}, fields["a_string"].GetKind())
	require.Equal(t, "hello", fields["a_string"].GetStringValue())

	// google.protobuf.Value.number_value is a double, so an integer
	// legitimately round-trips as a float (42 -> 42.0). That is inherent to
	// the proto, not a defect.
	require.IsType(t, &structpb.Value_NumberValue{}, fields["an_int"].GetKind())
	require.Equal(t, float64(42), fields["an_int"].GetNumberValue())

	require.IsType(t, &structpb.Value_NumberValue{}, fields["a_float"].GetKind())
	require.Equal(t, 3.5, fields["a_float"].GetNumberValue())

	require.IsType(t, &structpb.Value_BoolValue{}, fields["a_bool"].GetKind())
	require.True(t, fields["a_bool"].GetBoolValue())

	require.IsType(t, &structpb.Value_NullValue{}, fields["a_null"].GetKind())

	require.IsType(t, &structpb.Value_StructValue{}, fields["a_map"].GetKind())
	require.Equal(t, "value", fields["a_map"].GetStructValue().GetFields()["nested"].GetStringValue())

	require.IsType(t, &structpb.Value_ListValue{}, fields["a_list"].GetKind())
	listValues := fields["a_list"].GetListValue().GetValues()
	require.Len(t, listValues, 3)
	require.IsType(t, &structpb.Value_StringValue{}, listValues[0].GetKind())
	require.IsType(t, &structpb.Value_NumberValue{}, listValues[1].GetKind())
	require.IsType(t, &structpb.Value_BoolValue{}, listValues[2].GetKind())
}

// TestToProtoInvalidCaveatContextReturnsError is the regression test for the
// actual write-time defect: ToProto used to call structpb.NewStruct and
// discard the error, writing the relationship with the caveat name attached
// and an empty context. That corrupts the write silently and permanently —
// re-checking with correct context never repairs it, only rewriting the
// relationship does. ToProto must now return the error instead.
func TestToProtoInvalidCaveatContextReturnsError(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("some_caveat", map[string]any{
			// chan is not one of the types structpb.NewValue can represent.
			"unrepresentable": make(chan int),
		})

	proto, err := r.ToProto()
	require.Error(t, err)
	require.Nil(t, proto)
}

// TestFilterToProtoSubjectIDWithoutSubjectTypeReturnsError is the regression
// test for the offboarding hazard this finding describes: ToProto used to
// nest OptionalSubjectId inside the SubjectType check, so
// NewFilter("document").WithSubjectID("alice") produced a proto filter with
// no subject constraint at all -- DeleteRelationships called with that
// filter would delete every relationship on every document, not just
// alice's. ToProto must now return an error naming the missing field
// instead of silently widening the filter.
func TestFilterToProtoSubjectIDWithoutSubjectTypeReturnsError(t *testing.T) {
	f := rel.NewFilter("document").WithSubjectID("alice")

	proto, err := f.ToProto()

	require.Error(t, err)
	require.Nil(t, proto)
	require.ErrorIs(t, err, rel.ErrInvalidFilter)
	require.Contains(t, err.Error(), "SubjectID")
	require.Contains(t, err.Error(), "SubjectType")
}

// TestFilterToProtoSubjectRelationWithoutSubjectTypeReturnsError is the
// SubjectRelation counterpart of the above -- the wire's
// SubjectFilter.subject_type is required whenever any subject constraint
// (ID or relation) is present.
func TestFilterToProtoSubjectRelationWithoutSubjectTypeReturnsError(t *testing.T) {
	f := rel.NewFilter("document").WithSubjectRelation("member")

	proto, err := f.ToProto()

	require.Error(t, err)
	require.Nil(t, proto)
	require.ErrorIs(t, err, rel.ErrInvalidFilter)
	require.Contains(t, err.Error(), "SubjectRelation")
	require.Contains(t, err.Error(), "SubjectType")
}

// TestFilterToProtoSubjectTypeAloneSucceeds is a companion to the two error
// cases above -- proves that SubjectType alone (no SubjectID) still builds a
// valid subject filter and is not accidentally caught by the new guard.
func TestFilterToProtoSubjectTypeAloneSucceeds(t *testing.T) {
	f := rel.NewFilter("document").WithSubjectType("user")

	proto, err := f.ToProto()

	require.NoError(t, err)
	require.NotNil(t, proto.GetOptionalSubjectFilter())
	require.Equal(t, "user", proto.GetOptionalSubjectFilter().GetSubjectType())
	require.Empty(t, proto.GetOptionalSubjectFilter().GetOptionalSubjectId())
}

// TestFilterToProtoSubjectTypeAndIDSucceeds proves the valid combination
// (SubjectType supplied alongside SubjectID) still works correctly.
func TestFilterToProtoSubjectTypeAndIDSucceeds(t *testing.T) {
	f := rel.NewFilter("document").WithSubjectType("user").WithSubjectID("alice")

	proto, err := f.ToProto()

	require.NoError(t, err)
	require.NotNil(t, proto.GetOptionalSubjectFilter())
	require.Equal(t, "user", proto.GetOptionalSubjectFilter().GetSubjectType())
	require.Equal(t, "alice", proto.GetOptionalSubjectFilter().GetOptionalSubjectId())
}

// TestUpdateFromProtoUnrecognizedOperationIsUnspecified proves that a watch
// operation this client does not recognize -- OPERATION_UNSPECIFIED, or a
// future operation value added after this client shipped -- maps to the named
// UpdateOperationUnspecified, never to a write.
//
// Root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail",
// clause 2: server-supplied values the client does not recognise MUST NOT
// raise, and MUST map to the safe, non-permissive default -- never a grant,
// and never a write. A cache or index mirror consuming the watch stream that
// upserted on an unrecognized operation could turn a delete it doesn't
// understand into a silent write.
func TestUpdateFromProtoUnrecognizedOperationIsUnspecified(t *testing.T) {
	relProto := &v1.Relationship{
		Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "doc1"},
		Relation: "viewer",
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"},
		},
	}

	for name, op := range map[string]v1.RelationshipUpdate_Operation{
		"explicit OPERATION_UNSPECIFIED": v1.RelationshipUpdate_OPERATION_UNSPECIFIED,
		"unknown future operation":       v1.RelationshipUpdate_Operation(9999),
	} {
		t.Run(name, func(t *testing.T) {
			u := rel.UpdateFromProto(&v1.RelationshipUpdate{
				Operation:    op,
				Relationship: relProto,
			})

			require.Equal(t, rel.UpdateOperationUnspecified, u.Operation)
			require.NotEqual(t, rel.UpdateOperationTouch, u.Operation,
				"an operation the client cannot interpret must never be reported as a write")
			require.Equal(t, "doc1", u.Relationship.ResourceID)
		})
	}
}

// TestUpdateFromProtoRecognizedOperations is the companion to the case above:
// the three operations this client does understand still map to themselves.
func TestUpdateFromProtoRecognizedOperations(t *testing.T) {
	for proto, want := range map[v1.RelationshipUpdate_Operation]rel.UpdateOperation{
		v1.RelationshipUpdate_OPERATION_CREATE: rel.UpdateOperationCreate,
		v1.RelationshipUpdate_OPERATION_TOUCH:  rel.UpdateOperationTouch,
		v1.RelationshipUpdate_OPERATION_DELETE: rel.UpdateOperationDelete,
	} {
		u := rel.UpdateFromProto(&v1.RelationshipUpdate{
			Operation: proto,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "doc1"},
				Relation: "viewer",
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"},
				},
			},
		})
		require.Equal(t, want, u.Operation, "operation %v", proto)
	}
}

// TestRelationshipToProtoUnconvertibleContextNamesKeyAndSentinel proves the
// caveat-context conversion failure is a matchable sentinel that names the
// offending key, the way its sibling rel.ErrInvalidFilter already was.
//
// structpb.NewStruct converts the whole map at once and reports only the
// value's Go type ("invalid type: chan int"), never which entry held it. On a
// context map with many keys that leaves the caller guessing which one to
// fix. C#, Java and Ruby all name the key for the same failure; rel now does
// too, by converting per key.
func TestRelationshipToProtoUnconvertibleContextNamesKeyAndSentinel(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("only_bizhours", map[string]any{
			"fine":     42,
			"alsook":   "yes",
			"offender": make(chan int),
		})

	p, err := r.ToProto()

	require.Error(t, err)
	require.Nil(t, p, "nothing may be produced for an unconvertible context")
	require.ErrorIs(t, err, rel.ErrInvalidCaveatContext)
	require.Contains(t, err.Error(), `"offender"`,
		"the error must name the offending key, not just the value's type")
	require.Contains(t, err.Error(), r.String(),
		"the error must still identify the relationship")
}

// TestTxnPropagatesUnconvertibleContextSentinel proves Txn.Create/Touch/Delete
// surface the sentinel unchanged and add nothing to the transaction, so a
// caller can match on it with errors.Is.
//
// The sentinel lives in rel rather than being wrapped as a *client.Error here
// because rel is deliberately client-independent (see the package doc) and
// client imports rel -- wrapping inside rel would be an import cycle. This is
// exactly how the sibling rel.ErrInvalidFilter behaves: rel returns the
// sentinel, and the client package wraps it as a *client.Error with
// CodeInvalidArgument at its own API boundaries (ImportRelationships,
// DeleteRelationships, ReadRelationships, checkItemFromRel).
func TestTxnPropagatesUnconvertibleContextSentinel(t *testing.T) {
	bad := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("only_bizhours", map[string]any{"offender": make(chan int)})

	for name, add := range map[string]func(*rel.Txn, rel.Relationship) error{
		"Create": (*rel.Txn).Create,
		"Touch":  (*rel.Txn).Touch,
		"Delete": (*rel.Txn).Delete,
	} {
		t.Run(name, func(t *testing.T) {
			var txn rel.Txn

			err := add(&txn, bad)

			require.Error(t, err)
			require.ErrorIs(t, err, rel.ErrInvalidCaveatContext)
			require.Contains(t, err.Error(), `"offender"`)
			require.Empty(t, txn.V1Updates,
				"a failed conversion must add nothing to the transaction")
		})
	}
}

// TestCaveatContextToStructConvertsEveryRepresentableKind is the happy-path
// companion: the per-key conversion must produce exactly what
// structpb.NewStruct would for values it can represent, including nested
// maps and lists.
func TestCaveatContextToStructConvertsEveryRepresentableKind(t *testing.T) {
	context := map[string]any{
		"num":    42,
		"str":    "hello",
		"boolen": true,
		"null":   nil,
		"list":   []any{1, "two", false},
		"nested": map[string]any{"inner": 7},
	}

	got, err := rel.CaveatContextToStruct(context)
	require.NoError(t, err)

	want, err := structpb.NewStruct(context)
	require.NoError(t, err)

	require.Equal(t, want.AsMap(), got.AsMap())
}

// TestCaveatContextToStructNilAndEmpty proves the converter's edge cases
// behave like structpb.NewStruct's: an empty map produces an empty Struct,
// not an error. (Relationship.ToProto never calls it with nil -- it checks
// CaveatContext != nil first -- but the exported helper must not panic.)
func TestCaveatContextToStructNilAndEmpty(t *testing.T) {
	empty, err := rel.CaveatContextToStruct(map[string]any{})
	require.NoError(t, err)
	require.Empty(t, empty.AsMap())

	fromNil, err := rel.CaveatContextToStruct(nil)
	require.NoError(t, err)
	require.Empty(t, fromNil.AsMap())
}
