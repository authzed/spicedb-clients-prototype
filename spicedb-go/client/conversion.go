package client

import (
	"fmt"

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

// malformed reports a response the client cannot interpret: a missing
// required field, an unset enum the server always sets, or a value no
// valid server response carries. It is INTERNAL, which no retry policy retries
// by default, and the response is never skipped or filled with empty values.
// The message says the client detected it, so it is not mistaken for a status
// the server sent.
func malformed(op, format string, args ...any) error {
	return &Error{Code: CodeInternal, Message: fmt.Sprintf("spicedb: %s: client detected malformed response from server: ", op) + fmt.Sprintf(format, args...)}
}

// malformedErr classifies a response conversion failure as malformed.
func malformedErr(op string, e error) error {
	if e == nil {
		return nil
	}
	m := malformed(op, "%s", e).(*Error)
	m.err = e
	return m
}

func nativeRelationship(op string, p *v1.Relationship) (rel.Relationship, error) {
	if p == nil {
		return rel.Relationship{}, malformed(op, "relationship is missing")
	}
	r, e := conv.NativeRelationship(p)
	return r, malformedErr(op, e)
}

// nativeObjectStrict converts a response object reference that must be
// present and valid.
func nativeObjectStrict(op, field string, p *v1.ObjectReference) (rel.ObjectRef, error) {
	if p == nil {
		return rel.ObjectRef{}, malformed(op, "%s is missing", field)
	}
	o, e := rel.NewObject(p.GetObjectType(), p.GetObjectId())
	if e != nil {
		return rel.ObjectRef{}, malformed(op, "%s: %s", field, e)
	}
	return o, nil
}

// nativeSubjectStrict converts a response subject reference that must be
// present and valid.
func nativeSubjectStrict(op, field string, p *v1.SubjectReference) (rel.SubjectRef, error) {
	if p == nil {
		return rel.SubjectRef{}, malformed(op, "%s is missing", field)
	}
	o, e := nativeObjectStrict(op, field+" object", p.GetObject())
	if e != nil {
		return rel.SubjectRef{}, e
	}
	s, e := rel.NewSubject(o, p.GetOptionalRelation())
	if e != nil {
		return rel.SubjectRef{}, malformed(op, "%s: %s", field, e)
	}
	return s, nil
}
func filter(f rel.Filter) (*v1.RelationshipFilter, error) { return invalidErr(conv.Filter(f)) }
func preconditions(ps []rel.Precondition) ([]*v1.Precondition, error) {
	return invalidErr(conv.Preconditions(ps))
}
func cursor(s string) *v1.Cursor { return conv.Cursor(s) }
func nativeUpdate(op string, p *v1.RelationshipUpdate) (rel.Update, error) {
	if p.GetRelationship() == nil {
		return rel.Update{}, malformed(op, "update relationship is missing")
	}
	u, e := conv.NativeUpdate(p)
	return u, malformedErr(op, e)
}
