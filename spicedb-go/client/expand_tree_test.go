package client

import (
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// TestExpandToPermissionTree constructs a synthetic proto PermissionRelationshipTree
// and verifies the recursive mapper produces the expected native
// PermissionTree structure, field-by-field.
//
// Synthetic shape:
//
//	root: intermediate UNION on document:doc1#view
//	  ├─ leaf with 2 subjects (one with OptionalRelation, one without)
//	  └─ intermediate INTERSECTION on document:doc1#view
//	       └─ leaf with 1 subject
func TestExpandToPermissionTree(t *testing.T) {
	innerLeaf := &v1.PermissionRelationshipTree{
		ExpandedObject: &v1.ObjectReference{
			ObjectType: "document",
			ObjectId:   "doc1",
		},
		ExpandedRelation: "view",
		TreeType: &v1.PermissionRelationshipTree_Leaf{
			Leaf: &v1.DirectSubjectSet{
				Subjects: []*v1.SubjectReference{
					{
						Object: &v1.ObjectReference{
							ObjectType: "user",
							ObjectId:   "carol",
						},
					},
				},
			},
		},
	}

	innerIntermediate := &v1.PermissionRelationshipTree{
		ExpandedObject: &v1.ObjectReference{
			ObjectType: "document",
			ObjectId:   "doc1",
		},
		ExpandedRelation: "view",
		TreeType: &v1.PermissionRelationshipTree_Intermediate{
			Intermediate: &v1.AlgebraicSubjectSet{
				Operation: v1.AlgebraicSubjectSet_OPERATION_INTERSECTION,
				Children:  []*v1.PermissionRelationshipTree{innerLeaf},
			},
		},
	}

	rootLeaf := &v1.PermissionRelationshipTree{
		ExpandedObject: &v1.ObjectReference{
			ObjectType: "document",
			ObjectId:   "doc1",
		},
		ExpandedRelation: "view",
		TreeType: &v1.PermissionRelationshipTree_Leaf{
			Leaf: &v1.DirectSubjectSet{
				Subjects: []*v1.SubjectReference{
					{
						Object: &v1.ObjectReference{
							ObjectType: "user",
							ObjectId:   "alice",
						},
						OptionalRelation: "member",
					},
					{
						Object: &v1.ObjectReference{
							ObjectType: "user",
							ObjectId:   "bob",
						},
					},
				},
			},
		},
	}

	root := &v1.PermissionRelationshipTree{
		ExpandedObject: &v1.ObjectReference{
			ObjectType: "document",
			ObjectId:   "doc1",
		},
		ExpandedRelation: "view",
		TreeType: &v1.PermissionRelationshipTree_Intermediate{
			Intermediate: &v1.AlgebraicSubjectSet{
				Operation: v1.AlgebraicSubjectSet_OPERATION_UNION,
				Children:  []*v1.PermissionRelationshipTree{rootLeaf, innerIntermediate},
			},
		},
	}

	got := toPermissionTree(root)

	want := PermissionTree{
		ExpandedObject:   ObjectRef{ObjectType: "document", ObjectID: "doc1"},
		ExpandedRelation: "view",
		Intermediate: &IntermediateNode{
			Operation: TreeOperationUnion,
			Children: []PermissionTree{
				{
					ExpandedObject:   ObjectRef{ObjectType: "document", ObjectID: "doc1"},
					ExpandedRelation: "view",
					Leaf: &LeafNode{
						Subjects: []SubjectRef{
							{
								SubjectType:      "user",
								SubjectID:        "alice",
								OptionalRelation: "member",
							},
							{
								SubjectType: "user",
								SubjectID:   "bob",
							},
						},
					},
				},
				{
					ExpandedObject:   ObjectRef{ObjectType: "document", ObjectID: "doc1"},
					ExpandedRelation: "view",
					Intermediate: &IntermediateNode{
						Operation: TreeOperationIntersection,
						Children: []PermissionTree{
							{
								ExpandedObject:   ObjectRef{ObjectType: "document", ObjectID: "doc1"},
								ExpandedRelation: "view",
								Leaf: &LeafNode{
									Subjects: []SubjectRef{
										{
											SubjectType: "user",
											SubjectID:   "carol",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	require.Equal(t, want, got)
}

// TestExpandToPermissionTreeNil verifies a nil proto tree maps to a zero-value
// native node rather than panicking.
func TestExpandToPermissionTreeNil(t *testing.T) {
	got := toPermissionTree(nil)
	require.Equal(t, PermissionTree{}, got)
}

// TestExpandToPermissionTreeUnspecifiedOperation verifies the unspecified
// algebraic operation maps to TreeOperationUnspecified.
func TestExpandToPermissionTreeUnspecifiedOperation(t *testing.T) {
	root := &v1.PermissionRelationshipTree{
		TreeType: &v1.PermissionRelationshipTree_Intermediate{
			Intermediate: &v1.AlgebraicSubjectSet{
				Operation: v1.AlgebraicSubjectSet_OPERATION_UNSPECIFIED,
			},
		},
	}

	got := toPermissionTree(root)
	require.NotNil(t, got.Intermediate)
	require.Equal(t, TreeOperationUnspecified, got.Intermediate.Operation)
}

// TestExpandToPermissionTreeExclusion verifies the exclusion algebraic operation
// maps to TreeOperationExclusion.
func TestExpandToPermissionTreeExclusion(t *testing.T) {
	root := &v1.PermissionRelationshipTree{
		TreeType: &v1.PermissionRelationshipTree_Intermediate{
			Intermediate: &v1.AlgebraicSubjectSet{
				Operation: v1.AlgebraicSubjectSet_OPERATION_EXCLUSION,
			},
		},
	}

	got := toPermissionTree(root)
	require.NotNil(t, got.Intermediate)
	require.Equal(t, TreeOperationExclusion, got.Intermediate.Operation)
}
