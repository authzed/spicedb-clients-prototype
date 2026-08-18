package client

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// ObjectRef identifies a resource or subject object.
type ObjectRef struct {
	ObjectType string
	ObjectID   string
}

// TreeOperation is the set operation combining an intermediate node's children.
type TreeOperation int

const (
	TreeOperationUnspecified TreeOperation = iota
	TreeOperationUnion
	TreeOperationIntersection
	TreeOperationExclusion
)

// SubjectRef is a subject with access at a leaf of the tree.
type SubjectRef struct {
	SubjectType      string
	SubjectID        string
	OptionalRelation string // empty if none
}

// IntermediateNode combines child subtrees with a set operation.
type IntermediateNode struct {
	Operation TreeOperation
	Children  []PermissionTree
}

// LeafNode holds the concrete subjects at a leaf.
type LeafNode struct {
	Subjects []SubjectRef
}

// PermissionTree is a native node of an expanded permission tree. Exactly one
// of Intermediate or Leaf is non-nil.
type PermissionTree struct {
	ExpandedObject   ObjectRef
	ExpandedRelation string
	Intermediate     *IntermediateNode
	Leaf             *LeafNode
}

// toPermissionTree recursively maps a proto PermissionRelationshipTree to its
// native representation. A nil input maps to a zero-value PermissionTree.
func toPermissionTree(t *v1.PermissionRelationshipTree) PermissionTree {
	if t == nil {
		return PermissionTree{}
	}

	tree := PermissionTree{
		ExpandedObject: ObjectRef{
			ObjectType: t.GetExpandedObject().GetObjectType(),
			ObjectID:   t.GetExpandedObject().GetObjectId(),
		},
		ExpandedRelation: t.GetExpandedRelation(),
	}

	if intermediate := t.GetIntermediate(); intermediate != nil {
		children := make([]PermissionTree, 0, len(intermediate.GetChildren()))
		for _, child := range intermediate.GetChildren() {
			children = append(children, toPermissionTree(child))
		}
		tree.Intermediate = &IntermediateNode{
			Operation: toTreeOperation(intermediate.GetOperation()),
			Children:  children,
		}
	}

	if leaf := t.GetLeaf(); leaf != nil {
		subjects := make([]SubjectRef, 0, len(leaf.GetSubjects()))
		for _, subject := range leaf.GetSubjects() {
			subjects = append(subjects, SubjectRef{
				SubjectType:      subject.GetObject().GetObjectType(),
				SubjectID:        subject.GetObject().GetObjectId(),
				OptionalRelation: subject.GetOptionalRelation(),
			})
		}
		tree.Leaf = &LeafNode{Subjects: subjects}
	}

	return tree
}

// toTreeOperation maps the proto algebraic set operation to its native
// equivalent.
func toTreeOperation(op v1.AlgebraicSubjectSet_Operation) TreeOperation {
	switch op {
	case v1.AlgebraicSubjectSet_OPERATION_UNION:
		return TreeOperationUnion
	case v1.AlgebraicSubjectSet_OPERATION_INTERSECTION:
		return TreeOperationIntersection
	case v1.AlgebraicSubjectSet_OPERATION_EXCLUSION:
		return TreeOperationExclusion
	default:
		return TreeOperationUnspecified
	}
}
