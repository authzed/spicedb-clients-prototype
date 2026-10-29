package client

import (
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
)

func TestExpandNativeTaggedTree(t *testing.T) {
	leaf := &v1.PermissionRelationshipTree{ExpandedObject: &v1.ObjectReference{ObjectType: "document", ObjectId: "d"}, ExpandedRelation: "viewer", TreeType: &v1.PermissionRelationshipTree_Leaf{Leaf: &v1.DirectSubjectSet{Subjects: []*v1.SubjectReference{{Object: &v1.ObjectReference{ObjectType: "group", ObjectId: "eng"}, OptionalRelation: "member"}}}}}
	tree, err := toPermissionTree(&v1.PermissionRelationshipTree{TreeType: &v1.PermissionRelationshipTree_Intermediate{Intermediate: &v1.AlgebraicSubjectSet{Operation: v1.AlgebraicSubjectSet_OPERATION_UNION, Children: []*v1.PermissionRelationshipTree{leaf}}}})
	require.NoError(t, err)
	require.Equal(t, PermissionTreeIntermediate, tree.Kind())
	require.Equal(t, TreeOperationUnion, tree.Operation())
	require.Nil(t, tree.Subjects())
	children := tree.Children()
	require.Len(t, children, 1)
	l := children[0]
	require.Equal(t, PermissionTreeLeaf, l.Kind())
	require.Equal(t, "document", l.ExpandedObject().Type())
	require.Equal(t, "viewer", l.ExpandedRelation())
	require.Equal(t, "member", l.Subjects()[0].Relation())
	children[0] = PermissionTree{}
	require.Equal(t, PermissionTreeLeaf, tree.Children()[0].Kind())
	unknown, err := toPermissionTree(&v1.PermissionRelationshipTree{})
	require.NoError(t, err)
	require.Equal(t, PermissionTreeUnknown, unknown.Kind())
	for _, tc := range []struct {
		v v1.AlgebraicSubjectSet_Operation
		w TreeOperation
	}{{v1.AlgebraicSubjectSet_OPERATION_UNION, TreeOperationUnion}, {v1.AlgebraicSubjectSet_OPERATION_INTERSECTION, TreeOperationIntersection}, {v1.AlgebraicSubjectSet_OPERATION_EXCLUSION, TreeOperationExclusion}, {v1.AlgebraicSubjectSet_Operation(999), TreeOperationUnspecified}} {
		require.Equal(t, tc.w, toTreeOperation(tc.v))
	}
}
