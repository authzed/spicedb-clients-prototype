package client

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

type TreeOperation uint8

const (
	TreeOperationUnspecified TreeOperation = iota
	TreeOperationUnion
	TreeOperationIntersection
	TreeOperationExclusion
)

type PermissionTreeKind uint8

const (
	PermissionTreeUnknown PermissionTreeKind = iota
	PermissionTreeIntermediate
	PermissionTreeLeaf
)

// PermissionTree is an opaque tagged tree. Its zero value is Unknown.
type PermissionTree struct {
	kind             PermissionTreeKind
	expandedObject   rel.ObjectRef
	expandedRelation string
	operation        TreeOperation
	children         []PermissionTree
	subjects         []rel.SubjectRef
}

func (t PermissionTree) Kind() PermissionTreeKind      { return t.kind }
func (t PermissionTree) ExpandedObject() rel.ObjectRef { return t.expandedObject }
func (t PermissionTree) ExpandedRelation() string      { return t.expandedRelation }
func (t PermissionTree) Operation() TreeOperation      { return t.operation }
func (t PermissionTree) Children() []PermissionTree {
	return append([]PermissionTree(nil), t.children...)
}
func (t PermissionTree) Subjects() []rel.SubjectRef {
	return append([]rel.SubjectRef(nil), t.subjects...)
}

// toPermissionTree converts a tree node. A node whose variant this client
// does not know is Unknown; the expanded object is optional (set operations
// nested inside one rewrite carry none). A leaf subject that is missing or
// invalid is a malformed response, never an empty subject.
func toPermissionTree(p *v1.PermissionRelationshipTree) (PermissionTree, error) {
	if p == nil {
		return PermissionTree{}, nil
	}
	t := PermissionTree{expandedObject: nativeObject(p.GetExpandedObject()), expandedRelation: p.GetExpandedRelation()}
	if inner := p.GetIntermediate(); inner != nil {
		t.kind = PermissionTreeIntermediate
		t.operation = toTreeOperation(inner.GetOperation())
		for _, c := range inner.GetChildren() {
			child, e := toPermissionTree(c)
			if e != nil {
				return PermissionTree{}, e
			}
			t.children = append(t.children, child)
		}
	} else if leaf := p.GetLeaf(); leaf != nil {
		t.kind = PermissionTreeLeaf
		for _, s := range leaf.GetSubjects() {
			subject, e := nativeSubjectStrict("expand permission tree", "leaf subject", s)
			if e != nil {
				return PermissionTree{}, e
			}
			t.subjects = append(t.subjects, subject)
		}
	}
	return t, nil
}
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
