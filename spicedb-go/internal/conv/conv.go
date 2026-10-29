// Package conv converts native relationship-level values to and from the
// protocol. The client package uses it on the wire, and client/protoconv
// re-exports it. Errors describe the offending value ("relationship: ...");
// callers decide how to classify them.
package conv

import (
	"errors"
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/internal/native"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Strategy converts a consistency strategy.
func Strategy(s consistency.Strategy) (*v1.Consistency, error) {
	if e := s.Validate(); e != nil {
		return nil, fmt.Errorf("consistency: %s", e)
	}
	p := &v1.Consistency{}
	switch s.Kind() {
	case consistency.FullyConsistent:
		p.Requirement = &v1.Consistency_FullyConsistent{FullyConsistent: true}
	case consistency.MinimizeLatency:
		p.Requirement = &v1.Consistency_MinimizeLatency{MinimizeLatency: true}
	case consistency.AtLeastAsFresh:
		p.Requirement = &v1.Consistency_AtLeastAsFresh{AtLeastAsFresh: ZedToken(s.Token())}
	case consistency.ExactSnapshot:
		p.Requirement = &v1.Consistency_AtExactSnapshot{AtExactSnapshot: ZedToken(s.Token())}
	}
	return p, nil
}

// NativeStrategy converts a protocol consistency requirement.
func NativeStrategy(p *v1.Consistency) (consistency.Strategy, error) {
	switch r := p.GetRequirement().(type) {
	case *v1.Consistency_FullyConsistent:
		return consistency.Full(), nil
	case *v1.Consistency_MinimizeLatency:
		return consistency.MinLatency(), nil
	case *v1.Consistency_AtLeastAsFresh:
		return consistency.AtLeast(NativeZedToken(r.AtLeastAsFresh)), nil
	case *v1.Consistency_AtExactSnapshot:
		return consistency.Snapshot(NativeZedToken(r.AtExactSnapshot)), nil
	}
	return consistency.Strategy{}, errors.New("consistency: no requirement")
}

// ZedToken converts a token; the zero token converts to nil.
func ZedToken(t consistency.ZedToken) *v1.ZedToken {
	if t.String() == "" {
		return nil
	}
	return &v1.ZedToken{Token: t.String()}
}

// NativeZedToken converts a token; nil converts to the zero token.
func NativeZedToken(p *v1.ZedToken) consistency.ZedToken {
	return consistency.NewZedToken(p.GetToken())
}

func Object(o rel.ObjectRef) *v1.ObjectReference {
	return &v1.ObjectReference{ObjectType: o.Type(), ObjectId: o.ID()}
}

func Subject(s rel.SubjectRef) *v1.SubjectReference {
	return &v1.SubjectReference{Object: Object(s.Object()), OptionalRelation: s.Relation()}
}

// NativeObject converts an object reference without validating it.
func NativeObject(o *v1.ObjectReference) rel.ObjectRef {
	r, _ := rel.NewObject(o.GetObjectType(), o.GetObjectId())
	return r
}

// NativeSubject converts a subject reference without validating it.
func NativeSubject(s *v1.SubjectReference) rel.SubjectRef {
	r, _ := rel.NewSubject(NativeObject(s.GetObject()), s.GetOptionalRelation())
	return r
}

// ContextStruct converts a native context map; nil converts to nil.
func ContextStruct(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}
	snapshot, e := native.Map(m)
	if e != nil {
		return nil, fmt.Errorf("context %s", e)
	}
	p, e := structpb.NewStruct(snapshot)
	if e != nil {
		return nil, fmt.Errorf("context: %s", e)
	}
	return p, nil
}

func Relationship(r rel.Relationship) (*v1.Relationship, error) {
	if e := r.Validate(); e != nil {
		return nil, fmt.Errorf("relationship: %s", e)
	}
	p := &v1.Relationship{Resource: Object(r.Resource()), Relation: r.Relation(), Subject: Subject(r.Subject())}
	if r.CaveatName() != "" {
		ctx, e := ContextStruct(r.CaveatContext())
		if e != nil {
			return nil, e
		}
		p.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName: r.CaveatName(), Context: ctx}
	}
	if exp := r.Expiration(); exp != nil {
		p.OptionalExpiresAt = timestamppb.New(*exp)
		if e := p.OptionalExpiresAt.CheckValid(); e != nil {
			return nil, fmt.Errorf("expiration: %s", e)
		}
	}
	return p, nil
}

func NativeRelationship(p *v1.Relationship) (rel.Relationship, error) {
	r, e := rel.NewRelationship(NativeObject(p.GetResource()), p.GetRelation(), NativeSubject(p.GetSubject()))
	if e != nil {
		return r, fmt.Errorf("spicedb: malformed server relationship: %w", e)
	}
	if cav := p.GetOptionalCaveat(); cav != nil {
		r, e = r.WithCaveat(cav.GetCaveatName(), cav.GetContext().AsMap())
		if e != nil {
			return r, e
		}
	}
	if exp := p.GetOptionalExpiresAt(); exp != nil {
		r = r.WithExpiration(exp.AsTime())
	}
	return r, nil
}

func Filter(f rel.Filter) (*v1.RelationshipFilter, error) {
	if e := f.Validate(); e != nil {
		return nil, fmt.Errorf("filter: %s", e)
	}
	p := &v1.RelationshipFilter{ResourceType: f.ResourceType(), OptionalResourceId: f.ResourceID(), OptionalResourceIdPrefix: f.ResourceIDPrefix(), OptionalRelation: f.Relation()}
	if f.SubjectType() != "" {
		p.OptionalSubjectFilter = &v1.SubjectFilter{SubjectType: f.SubjectType(), OptionalSubjectId: f.SubjectID()}
		if rf := f.SubjectRelation(); rf.Kind() != rel.RelationAny {
			p.OptionalSubjectFilter.OptionalRelation = &v1.SubjectFilter_RelationFilter{Relation: rf.Name()}
		}
	}
	return p, nil
}

// NativeFilter converts a protocol filter, preserving the Any/Direct/Named
// subject relation distinction.
func NativeFilter(p *v1.RelationshipFilter) (rel.Filter, error) {
	f, e := rel.NewFilter(p.GetResourceType())
	if e != nil {
		return rel.Filter{}, fmt.Errorf("filter: %s", e)
	}
	f = f.WithResourceID(p.GetOptionalResourceId()).WithResourceIDPrefix(p.GetOptionalResourceIdPrefix()).WithRelation(p.GetOptionalRelation())
	if sf := p.GetOptionalSubjectFilter(); sf != nil {
		f = f.WithSubjectType(sf.GetSubjectType()).WithSubjectID(sf.GetOptionalSubjectId())
		if rf := sf.GetOptionalRelation(); rf != nil {
			if rf.GetRelation() == "" {
				f = f.WithSubjectRelation(rel.DirectRelation())
			} else {
				named, e := rel.NamedRelation(rf.GetRelation())
				if e != nil {
					return rel.Filter{}, fmt.Errorf("filter: %s", e)
				}
				f = f.WithSubjectRelation(named)
			}
		}
	}
	if e := f.Validate(); e != nil {
		return rel.Filter{}, fmt.Errorf("filter: %s", e)
	}
	return f, nil
}

func Preconditions(ps []rel.Precondition) ([]*v1.Precondition, error) {
	out := make([]*v1.Precondition, len(ps))
	for i, p := range ps {
		f, e := Filter(p.Filter)
		if e != nil {
			return nil, e
		}
		op := v1.Precondition_OPERATION_MUST_NOT_MATCH
		if p.Operation != rel.PreconditionMustMatch && p.Operation != rel.PreconditionMustNotMatch {
			return nil, errors.New("unknown precondition operation")
		}
		if p.Operation == rel.PreconditionMustMatch {
			op = v1.Precondition_OPERATION_MUST_MATCH
		}
		out[i] = &v1.Precondition{Filter: f, Operation: op}
	}
	return out, nil
}

func NativePrecondition(p *v1.Precondition) (rel.Precondition, error) {
	f, e := NativeFilter(p.GetFilter())
	if e != nil {
		return rel.Precondition{}, e
	}
	switch p.GetOperation() {
	case v1.Precondition_OPERATION_MUST_MATCH:
		return rel.Precondition{Filter: f, Operation: rel.PreconditionMustMatch}, nil
	case v1.Precondition_OPERATION_MUST_NOT_MATCH:
		return rel.Precondition{Filter: f, Operation: rel.PreconditionMustNotMatch}, nil
	}
	return rel.Precondition{}, errors.New("unknown precondition operation")
}

func Cursor(s string) *v1.Cursor {
	if s == "" {
		return nil
	}
	return &v1.Cursor{Token: s}
}

func Update(u rel.Update) (*v1.RelationshipUpdate, error) {
	r, e := Relationship(u.Relationship)
	if e != nil {
		return nil, e
	}
	var op v1.RelationshipUpdate_Operation
	switch u.Operation {
	case rel.UpdateOperationCreate:
		op = v1.RelationshipUpdate_OPERATION_CREATE
	case rel.UpdateOperationTouch:
		op = v1.RelationshipUpdate_OPERATION_TOUCH
	case rel.UpdateOperationDelete:
		op = v1.RelationshipUpdate_OPERATION_DELETE
	default:
		return nil, errors.New("unknown mutation operation")
	}
	return &v1.RelationshipUpdate{Operation: op, Relationship: r}, nil
}

func NativeUpdate(p *v1.RelationshipUpdate) (rel.Update, error) {
	r, e := NativeRelationship(p.GetRelationship())
	op := rel.UpdateOperationUnspecified
	switch p.GetOperation() {
	case v1.RelationshipUpdate_OPERATION_CREATE:
		op = rel.UpdateOperationCreate
	case v1.RelationshipUpdate_OPERATION_TOUCH:
		op = rel.UpdateOperationTouch
	case v1.RelationshipUpdate_OPERATION_DELETE:
		op = rel.UpdateOperationDelete
	}
	return rel.Update{Operation: op, Relationship: r}, e
}
