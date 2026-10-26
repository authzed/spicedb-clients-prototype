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
func toPermissionTree(p *v1.PermissionRelationshipTree) PermissionTree {
	if p == nil {
		return PermissionTree{}
	}
	t := PermissionTree{expandedObject: nativeObject(p.GetExpandedObject()), expandedRelation: p.GetExpandedRelation()}
	if inner := p.GetIntermediate(); inner != nil {
		t.kind = PermissionTreeIntermediate
		t.operation = toTreeOperation(inner.GetOperation())
		for _, c := range inner.GetChildren() {
			t.children = append(t.children, toPermissionTree(c))
		}
	} else if leaf := p.GetLeaf(); leaf != nil {
		t.kind = PermissionTreeLeaf
		for _, s := range leaf.GetSubjects() {
			t.subjects = append(t.subjects, nativeSubject(s))
		}
	}
	return t
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
