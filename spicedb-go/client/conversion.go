package client

import (
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func strategy(s consistency.Strategy) (*v1.Consistency, error) {
	if e := s.Validate(); e != nil {
		return nil, invalid("consistency: %s", e)
	}
	p := &v1.Consistency{}
	switch s.Kind() {
	case consistency.FullyConsistent:
		p.Requirement = &v1.Consistency_FullyConsistent{FullyConsistent: true}
	case consistency.MinimizeLatency:
		p.Requirement = &v1.Consistency_MinimizeLatency{MinimizeLatency: true}
	case consistency.AtLeastAsFresh:
		p.Requirement = &v1.Consistency_AtLeastAsFresh{AtLeastAsFresh: &v1.ZedToken{Token: s.Token().String()}}
	case consistency.ExactSnapshot:
		p.Requirement = &v1.Consistency_AtExactSnapshot{AtExactSnapshot: &v1.ZedToken{Token: s.Token().String()}}
	}
	return p, nil
}
func object(o rel.ObjectRef) *v1.ObjectReference {
	return &v1.ObjectReference{ObjectType: o.Type(), ObjectId: o.ID()}
}
func subject(s rel.SubjectRef) *v1.SubjectReference {
	return &v1.SubjectReference{Object: object(s.Object()), OptionalRelation: s.Relation()}
}
func nativeObject(o *v1.ObjectReference) rel.ObjectRef {
	r, _ := rel.NewObject(o.GetObjectType(), o.GetObjectId())
	return r
}
func nativeSubject(s *v1.SubjectReference) rel.SubjectRef {
	r, _ := rel.NewSubject(nativeObject(s.GetObject()), s.GetOptionalRelation())
	return r
}
func contextStruct(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}
	snapshot, e := snapshotContext(m)
	if e != nil {
		return nil, e
	}
	p, e := structpb.NewStruct(snapshot)
	if e != nil {
		return nil, invalid("context: %s", e)
	}
	return p, nil
}
func relationship(r rel.Relationship) (*v1.Relationship, error) {
	if e := r.Validate(); e != nil {
		return nil, invalid("relationship: %s", e)
	}
	p := &v1.Relationship{Resource: object(r.Resource()), Relation: r.Relation(), Subject: subject(r.Subject())}
	if r.CaveatName() != "" {
		ctx, e := contextStruct(r.CaveatContext())
		if e != nil {
			return nil, e
		}
		p.OptionalCaveat = &v1.ContextualizedCaveat{CaveatName: r.CaveatName(), Context: ctx}
	}
	if exp := r.Expiration(); exp != nil {
		p.OptionalExpiresAt = timestamppb.New(*exp)
		if e := p.OptionalExpiresAt.CheckValid(); e != nil {
			return nil, invalid("expiration: %s", e)
		}
	}
	return p, nil
}
func nativeRelationship(p *v1.Relationship) (rel.Relationship, error) {
	r, e := rel.NewRelationship(nativeObject(p.GetResource()), p.GetRelation(), nativeSubject(p.GetSubject()))
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
func filter(f rel.Filter) (*v1.RelationshipFilter, error) {
	if e := f.Validate(); e != nil {
		return nil, invalid("filter: %s", e)
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
func preconditions(ps []rel.Precondition) ([]*v1.Precondition, error) {
	out := make([]*v1.Precondition, len(ps))
	for i, p := range ps {
		f, e := filter(p.Filter)
		if e != nil {
			return nil, e
		}
		op := v1.Precondition_OPERATION_MUST_NOT_MATCH
		if p.Operation != rel.PreconditionMustMatch && p.Operation != rel.PreconditionMustNotMatch {
			return nil, invalid("unknown precondition operation")
		}
		if p.Operation == rel.PreconditionMustMatch {
			op = v1.Precondition_OPERATION_MUST_MATCH
		}
		out[i] = &v1.Precondition{Filter: f, Operation: op}
	}
	return out, nil
}
func cursor(s string) *v1.Cursor {
	if s == "" {
		return nil
	}
	return &v1.Cursor{Token: s}
}
func nativeUpdate(p *v1.RelationshipUpdate) (rel.Update, error) {
	r, e := nativeRelationship(p.GetRelationship())
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
