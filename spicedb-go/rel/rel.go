// Package rel provides immutable native tuple values and mutable transaction builders.
package rel

import (
	"fmt"
	"strings"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/internal/native"
)

var (
	ErrInvalidResource      = fmt.Errorf("resource type and id are required")
	ErrInvalidRelation      = fmt.Errorf("relation is required")
	ErrInvalidSubject       = fmt.Errorf("subject type and id are required")
	ErrInvalidFilter        = fmt.Errorf("invalid relationship filter")
	ErrInvalidCaveatContext = fmt.Errorf("invalid caveat context")
)

type ObjectRef struct{ objectType, objectID string }

func NewObject(objectType, objectID string) (ObjectRef, error) {
	if objectType == "" || objectID == "" {
		return ObjectRef{}, ErrInvalidResource
	}
	return ObjectRef{objectType, objectID}, nil
}
func (o ObjectRef) Type() string { return o.objectType }
func (o ObjectRef) ID() string   { return o.objectID }
func (o ObjectRef) Validate() error {
	if o.objectType == "" || o.objectID == "" {
		return ErrInvalidResource
	}
	return nil
}

type SubjectRef struct {
	object   ObjectRef
	relation string
}

func NewSubject(object ObjectRef, relation string) (SubjectRef, error) {
	if object.Validate() != nil {
		return SubjectRef{}, ErrInvalidSubject
	}
	if relation == "..." {
		return SubjectRef{}, fmt.Errorf("use empty relation for direct subjects: %w", ErrInvalidSubject)
	}
	return SubjectRef{object, relation}, nil
}
func (s SubjectRef) Object() ObjectRef { return s.object }
func (s SubjectRef) Relation() string  { return s.relation }
func (s SubjectRef) Validate() error {
	if s.object.Validate() != nil {
		return ErrInvalidSubject
	}
	return nil
}

type Relationship struct {
	resource      ObjectRef
	relation      string
	subject       SubjectRef
	caveatName    string
	caveatContext map[string]any
	expiration    *time.Time
}

func NewRelationship(resource ObjectRef, relation string, subject SubjectRef) (Relationship, error) {
	r := Relationship{resource: resource, relation: relation, subject: subject}
	return r, r.Validate()
}
func (r Relationship) Validate() error {
	if r.expiration != nil && (r.expiration.Year() < 1 || r.expiration.Year() > 9999) {
		return fmt.Errorf("expiration outside supported timestamp range")
	}
	if e := r.resource.Validate(); e != nil {
		return e
	}
	if r.relation == "" {
		return ErrInvalidRelation
	}
	return r.subject.Validate()
}
func (r Relationship) Resource() ObjectRef           { return r.resource }
func (r Relationship) Relation() string              { return r.relation }
func (r Relationship) Subject() SubjectRef           { return r.subject }
func (r Relationship) CaveatName() string            { return r.caveatName }
func (r Relationship) CaveatContext() map[string]any { m, _ := native.Map(r.caveatContext); return m }
func (r Relationship) Expiration() *time.Time {
	if r.expiration == nil {
		return nil
	}
	t := *r.expiration
	return &t
}
func (r Relationship) WithCaveat(name string, ctx map[string]any) (Relationship, error) {
	if name == "" {
		return r, fmt.Errorf("caveat name is required: %w", ErrInvalidCaveatContext)
	}
	m, e := native.Map(ctx)
	if e != nil {
		return r, fmt.Errorf("%w: %w", ErrInvalidCaveatContext, e)
	}
	r.caveatName = name
	r.caveatContext = m
	return r, nil
}
func (r Relationship) WithExpiration(t time.Time) Relationship { r.expiration = &t; return r }
func (r Relationship) String() string {
	s := fmt.Sprintf("%s:%s#%s@%s:%s", r.resource.Type(), r.resource.ID(), r.relation, r.subject.Object().Type(), r.subject.Object().ID())
	if r.subject.relation != "" {
		s += "#" + r.subject.relation
	}
	return s
}
func FromTuple(tuple string) (Relationship, error) {
	parts := strings.Split(tuple, "@")
	if len(parts) != 2 {
		return Relationship{}, fmt.Errorf("tuple requires one @")
	}
	rr := strings.Split(parts[0], "#")
	if len(rr) != 2 {
		return Relationship{}, fmt.Errorf("tuple requires resource relation")
	}
	ro := strings.SplitN(rr[0], ":", 2)
	ss := strings.Split(parts[1], "#")
	so := strings.SplitN(ss[0], ":", 2)
	if len(ro) != 2 || len(so) != 2 || len(ss) > 2 {
		return Relationship{}, fmt.Errorf("invalid tuple")
	}
	o, e := NewObject(ro[0], ro[1])
	if e != nil {
		return Relationship{}, e
	}
	s, e := NewObject(so[0], so[1])
	if e != nil {
		return Relationship{}, e
	}
	relation := ""
	if len(ss) == 2 {
		relation = ss[1]
	}
	sub, e := NewSubject(s, relation)
	if e != nil {
		return Relationship{}, e
	}
	return NewRelationship(o, rr[1], sub)
}

type RelationFilterKind uint8

const (
	RelationAny RelationFilterKind = iota
	RelationDirect
	RelationNamed
)

type SubjectRelationFilter struct {
	kind RelationFilterKind
	name string
}

func AnyRelation() SubjectRelationFilter    { return SubjectRelationFilter{} }
func DirectRelation() SubjectRelationFilter { return SubjectRelationFilter{kind: RelationDirect} }
func NamedRelation(name string) (SubjectRelationFilter, error) {
	if name == "" || name == "..." {
		return SubjectRelationFilter{}, ErrInvalidRelation
	}
	return SubjectRelationFilter{RelationNamed, name}, nil
}
func (r SubjectRelationFilter) Kind() RelationFilterKind { return r.kind }
func (r SubjectRelationFilter) Name() string             { return r.name }

type Filter struct {
	resourceType, resourceID, resourceIDPrefix, relation, subjectType, subjectID string
	subjectRelation                                                              SubjectRelationFilter
}

func NewFilter(resourceType string) (Filter, error) {
	if resourceType == "" {
		return Filter{}, ErrInvalidFilter
	}
	return Filter{resourceType: resourceType}, nil
}
func (f Filter) WithResourceID(v string) Filter                     { f.resourceID = v; return f }
func (f Filter) WithResourceIDPrefix(v string) Filter               { f.resourceIDPrefix = v; return f }
func (f Filter) WithRelation(v string) Filter                       { f.relation = v; return f }
func (f Filter) WithSubjectType(v string) Filter                    { f.subjectType = v; return f }
func (f Filter) WithSubjectID(v string) Filter                      { f.subjectID = v; return f }
func (f Filter) WithSubjectRelation(v SubjectRelationFilter) Filter { f.subjectRelation = v; return f }
func (f Filter) ResourceType() string                               { return f.resourceType }
func (f Filter) ResourceID() string                                 { return f.resourceID }
func (f Filter) ResourceIDPrefix() string                           { return f.resourceIDPrefix }
func (f Filter) Relation() string                                   { return f.relation }
func (f Filter) SubjectType() string                                { return f.subjectType }
func (f Filter) SubjectID() string                                  { return f.subjectID }
func (f Filter) SubjectRelation() SubjectRelationFilter             { return f.subjectRelation }
func (f Filter) Validate() error {
	if f.resourceType == "" || (f.resourceID != "" && f.resourceIDPrefix != "") || (f.subjectType == "" && (f.subjectID != "" || f.subjectRelation.kind != RelationAny)) {
		return ErrInvalidFilter
	}
	return nil
}
func (r Relationship) Filter() Filter {
	rf := DirectRelation()
	if r.subject.relation != "" {
		rf, _ = NamedRelation(r.subject.relation)
	}
	return Filter{resourceType: r.resource.Type(), resourceID: r.resource.ID(), relation: r.relation, subjectType: r.subject.Object().Type(), subjectID: r.subject.Object().ID(), subjectRelation: rf}
}

type UpdateOperation uint8

const (
	UpdateOperationUnspecified UpdateOperation = iota
	UpdateOperationCreate
	UpdateOperationTouch
	UpdateOperationDelete
)

type Update struct {
	Operation    UpdateOperation
	Relationship Relationship
}

// PreconditionOperation selects whether a filter must match any relationships.
type PreconditionOperation uint8

const (
	PreconditionUnspecified PreconditionOperation = iota
	PreconditionMustMatch
	PreconditionMustNotMatch
)

type Precondition struct {
	Filter    Filter
	Operation PreconditionOperation
}

// Txn is a mutable transaction builder. Methods validate before adding native values.
type Txn struct {
	updates       []Update
	preconditions []Precondition
}

func (t *Txn) add(r Relationship, op UpdateOperation) error {
	if e := r.Validate(); e != nil {
		return e
	}
	t.updates = append(t.updates, Update{op, r})
	return nil
}
func (t *Txn) Create(r Relationship) error { return t.add(r, UpdateOperationCreate) }
func (t *Txn) Touch(r Relationship) error  { return t.add(r, UpdateOperationTouch) }
func (t *Txn) Delete(r Relationship) error { return t.add(r, UpdateOperationDelete) }
func (t *Txn) MustMatch(f Filter) error {
	if e := f.Validate(); e != nil {
		return e
	}
	t.preconditions = append(t.preconditions, Precondition{f, PreconditionMustMatch})
	return nil
}
func (t *Txn) MustNotMatch(f Filter) error {
	if e := f.Validate(); e != nil {
		return e
	}
	t.preconditions = append(t.preconditions, Precondition{f, PreconditionMustNotMatch})
	return nil
}
func (t Txn) Updates() []Update             { return append([]Update(nil), t.updates...) }
func (t Txn) Preconditions() []Precondition { return append([]Precondition(nil), t.preconditions...) }
