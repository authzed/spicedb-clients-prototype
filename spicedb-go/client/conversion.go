package client

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/internal/conv"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/protobuf/types/known/structpb"
)

// The conversions live in internal/conv so client/protoconv can export them;
// these wrappers classify their failures as invalid-argument *Error values.

func invalidErr[T any](v T, e error) (T, error) {
	if e != nil {
		var zero T
		return zero, invalid("%s", e)
	}
	return v, nil
}

func strategy(s consistency.Strategy) (*v1.Consistency, error) { return invalidErr(conv.Strategy(s)) }
func object(o rel.ObjectRef) *v1.ObjectReference               { return conv.Object(o) }
func subject(s rel.SubjectRef) *v1.SubjectReference            { return conv.Subject(s) }
func nativeObject(o *v1.ObjectReference) rel.ObjectRef         { return conv.NativeObject(o) }
func nativeSubject(s *v1.SubjectReference) rel.SubjectRef      { return conv.NativeSubject(s) }
func contextStruct(m map[string]any) (*structpb.Struct, error) {
	return invalidErr(conv.ContextStruct(m))
}
func relationship(r rel.Relationship) (*v1.Relationship, error) {
	return invalidErr(conv.Relationship(r))
}
func nativeRelationship(p *v1.Relationship) (rel.Relationship, error) {
	return conv.NativeRelationship(p)
}
func filter(f rel.Filter) (*v1.RelationshipFilter, error) { return invalidErr(conv.Filter(f)) }
func preconditions(ps []rel.Precondition) ([]*v1.Precondition, error) {
	return invalidErr(conv.Preconditions(ps))
}
func cursor(s string) *v1.Cursor                                { return conv.Cursor(s) }
func nativeUpdate(p *v1.RelationshipUpdate) (rel.Update, error) { return conv.NativeUpdate(p) }
