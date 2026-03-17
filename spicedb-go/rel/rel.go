// Package rel provides relationship types, filters, and transaction builders
// for SpiceDB.
//
// Types in this package are independent of the client — you can construct
// relationships and filters without importing the client package.
package rel

import (
	"fmt"
	"strings"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Sentinel errors for validation.
var (
	// ErrInvalidResource indicates a resource type, ID, or relation is empty.
	ErrInvalidResource = fmt.Errorf("resource type, id, and relation are required")
	// ErrInvalidRelation indicates a relation string is empty.
	ErrInvalidRelation = fmt.Errorf("relation is required")
	// ErrInvalidSubject indicates a subject type or ID is empty.
	ErrInvalidSubject = fmt.Errorf("subject type and id are required")
)

// Relationship is a flat representation of a SpiceDB relationship.
// It avoids nested proto types in favor of plain Go fields.
type Relationship struct {
	ResourceType     string
	ResourceID       string
	ResourceRelation string
	SubjectType      string
	SubjectID        string
	SubjectRelation  string
	CaveatName       string
	CaveatContext    map[string]any
	Expiration       *time.Time
}

// Interface allows user-defined domain types to be used as relationships.
type Interface interface {
	Relationship() Relationship
}

// FromTriple creates a Relationship from a resource type/ID/relation and subject
// type/ID/relation. Returns an error if any required field is empty.
func FromTriple(
	resourceType, resourceID, resourceRelation string,
	subjectType, subjectID, subjectRelation string,
) (Relationship, error) {
	if resourceType == "" || resourceID == "" || resourceRelation == "" {
		return Relationship{}, ErrInvalidResource
	}
	if subjectType == "" || subjectID == "" {
		return Relationship{}, ErrInvalidSubject
	}
	return Relationship{
		ResourceType:     resourceType,
		ResourceID:       resourceID,
		ResourceRelation: resourceRelation,
		SubjectType:      subjectType,
		SubjectID:        subjectID,
		SubjectRelation:  subjectRelation,
	}, nil
}

// MustFromTriple is like FromTriple but panics on error.
func MustFromTriple(
	resourceType, resourceID, resourceRelation string,
	subjectType, subjectID, subjectRelation string,
) Relationship {
	r, err := FromTriple(resourceType, resourceID, resourceRelation, subjectType, subjectID, subjectRelation)
	if err != nil {
		panic(err)
	}
	return r
}

// FromTuple parses a relationship from a string in the format:
//
//	resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]
func FromTuple(tuple string) (Relationship, error) {
	// Split resource from subject at @
	parts := strings.SplitN(tuple, "@", 2)
	if len(parts) != 2 {
		return Relationship{}, fmt.Errorf("invalid tuple format: missing '@' separator")
	}

	resourcePart, subjectPart := parts[0], parts[1]

	// Parse resource: type:id#relation
	resourceParts := strings.SplitN(resourcePart, "#", 2)
	if len(resourceParts) != 2 {
		return Relationship{}, fmt.Errorf("invalid tuple format: missing '#' in resource")
	}

	resourceTypeID := strings.SplitN(resourceParts[0], ":", 2)
	if len(resourceTypeID) != 2 {
		return Relationship{}, fmt.Errorf("invalid tuple format: missing ':' in resource type:id")
	}

	// Parse subject: type:id[#relation]
	var subjectType, subjectID, subjectRelation string
	subjectHash := strings.SplitN(subjectPart, "#", 2)
	subjectTypeID := strings.SplitN(subjectHash[0], ":", 2)
	if len(subjectTypeID) != 2 {
		return Relationship{}, fmt.Errorf("invalid tuple format: missing ':' in subject type:id")
	}
	subjectType = subjectTypeID[0]
	subjectID = subjectTypeID[1]
	if len(subjectHash) == 2 {
		subjectRelation = subjectHash[1]
	}

	return FromTriple(
		resourceTypeID[0], resourceTypeID[1], resourceParts[1],
		subjectType, subjectID, subjectRelation,
	)
}

// FromObjects creates a Relationship from a resource and subject ObjectReference-like
// pair with the given relation.
func FromObjects(
	resourceType, resourceID, relation string,
	subjectType, subjectID string,
) (Relationship, error) {
	return FromTriple(resourceType, resourceID, relation, subjectType, subjectID, "")
}

// WithCaveat returns a copy of the relationship with the given caveat.
func (r Relationship) WithCaveat(name string, context map[string]any) Relationship {
	r.CaveatName = name
	r.CaveatContext = context
	return r
}

// WithExpiration returns a copy of the relationship with the given expiration.
func (r Relationship) WithExpiration(t time.Time) Relationship {
	r.Expiration = &t
	return r
}

// String returns the tuple string representation of the relationship.
func (r Relationship) String() string {
	s := fmt.Sprintf("%s:%s#%s@%s:%s", r.ResourceType, r.ResourceID, r.ResourceRelation, r.SubjectType, r.SubjectID)
	if r.SubjectRelation != "" {
		s += "#" + r.SubjectRelation
	}
	return s
}

// ToProto converts a Relationship to its proto representation.
func (r Relationship) ToProto() *v1.Relationship {
	rel := &v1.Relationship{
		Resource: &v1.ObjectReference{
			ObjectType: r.ResourceType,
			ObjectId:   r.ResourceID,
		},
		Relation: r.ResourceRelation,
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{
				ObjectType: r.SubjectType,
				ObjectId:   r.SubjectID,
			},
			OptionalRelation: r.SubjectRelation,
		},
	}

	if r.CaveatName != "" {
		rel.OptionalCaveat = &v1.ContextualizedCaveat{
			CaveatName: r.CaveatName,
		}
		if r.CaveatContext != nil {
			ctx, err := structpb.NewStruct(r.CaveatContext)
			if err == nil {
				rel.OptionalCaveat.Context = ctx
			}
		}
	}

	if r.Expiration != nil {
		rel.OptionalExpiresAt = timestamppb.New(*r.Expiration)
	}

	return rel
}

// FromProto converts a proto Relationship to an idiomatic Relationship.
func FromProto(pr *v1.Relationship) Relationship {
	r := Relationship{
		ResourceType:     pr.GetResource().GetObjectType(),
		ResourceID:       pr.GetResource().GetObjectId(),
		ResourceRelation: pr.GetRelation(),
		SubjectType:      pr.GetSubject().GetObject().GetObjectType(),
		SubjectID:        pr.GetSubject().GetObject().GetObjectId(),
		SubjectRelation:  pr.GetSubject().GetOptionalRelation(),
	}
	if c := pr.GetOptionalCaveat(); c != nil {
		r.CaveatName = c.GetCaveatName()
		if c.GetContext() != nil {
			r.CaveatContext = c.GetContext().AsMap()
		}
	}
	if exp := pr.GetOptionalExpiresAt(); exp != nil {
		t := exp.AsTime()
		r.Expiration = &t
	}
	return r
}

// Filter specifies criteria for matching relationships.
type Filter struct {
	ResourceType     string
	ResourceID       string
	ResourceIDPrefix string
	Relation         string
	SubjectType      string
	SubjectID        string
	SubjectRelation  string

	// V1Filter exposes the underlying proto type for advanced use cases.
	V1Filter *v1.RelationshipFilter
}

// NewFilter creates a filter that matches relationships of the given resource type.
func NewFilter(resourceType string) Filter {
	return Filter{ResourceType: resourceType}
}

// WithResourceID narrows the filter to a specific resource ID.
func (f Filter) WithResourceID(id string) Filter {
	f.ResourceID = id
	return f
}

// WithResourceIDPrefix narrows the filter to resource IDs with the given prefix.
func (f Filter) WithResourceIDPrefix(prefix string) Filter {
	f.ResourceIDPrefix = prefix
	return f
}

// WithRelation narrows the filter to a specific relation.
func (f Filter) WithRelation(relation string) Filter {
	f.Relation = relation
	return f
}

// WithSubjectType narrows the filter to a specific subject type.
func (f Filter) WithSubjectType(subjectType string) Filter {
	f.SubjectType = subjectType
	return f
}

// WithSubjectID narrows the filter to a specific subject ID.
func (f Filter) WithSubjectID(subjectID string) Filter {
	f.SubjectID = subjectID
	return f
}

// WithSubjectRelation narrows the filter to a specific subject relation.
func (f Filter) WithSubjectRelation(relation string) Filter {
	f.SubjectRelation = relation
	return f
}

// ToProto converts a Filter to its proto representation.
func (f Filter) ToProto() *v1.RelationshipFilter {
	if f.V1Filter != nil {
		return f.V1Filter
	}

	filter := &v1.RelationshipFilter{
		ResourceType:       f.ResourceType,
		OptionalResourceId: f.ResourceID,
		OptionalRelation:   f.Relation,
	}
	if f.ResourceIDPrefix != "" {
		filter.OptionalResourceIdPrefix = f.ResourceIDPrefix
	}
	if f.SubjectType != "" {
		filter.OptionalSubjectFilter = &v1.SubjectFilter{
			SubjectType:       f.SubjectType,
			OptionalSubjectId: f.SubjectID,
		}
		if f.SubjectRelation != "" {
			filter.OptionalSubjectFilter.OptionalRelation = &v1.SubjectFilter_RelationFilter{
				Relation: f.SubjectRelation,
			}
		}
	}
	return filter
}

// Relationship.Filter returns a Filter that matches the exact resource of this relationship.
func (r Relationship) Filter() Filter {
	return Filter{
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		Relation:     r.ResourceRelation,
		SubjectType:  r.SubjectType,
		SubjectID:    r.SubjectID,
	}
}

// Update represents a relationship mutation (create, touch, or delete).
type Update struct {
	Operation    UpdateOperation
	Relationship Relationship
}

// UpdateOperation is the type of mutation in an Update.
type UpdateOperation int

const (
	UpdateOperationCreate UpdateOperation = iota + 1
	UpdateOperationTouch
	UpdateOperationDelete
)

func (op UpdateOperation) toProto() v1.RelationshipUpdate_Operation {
	switch op {
	case UpdateOperationCreate:
		return v1.RelationshipUpdate_OPERATION_CREATE
	case UpdateOperationTouch:
		return v1.RelationshipUpdate_OPERATION_TOUCH
	case UpdateOperationDelete:
		return v1.RelationshipUpdate_OPERATION_DELETE
	default:
		return v1.RelationshipUpdate_OPERATION_UNSPECIFIED
	}
}

// UpdateFromProto converts a proto RelationshipUpdate to an idiomatic Update.
func UpdateFromProto(pu *v1.RelationshipUpdate) Update {
	var op UpdateOperation
	switch pu.GetOperation() {
	case v1.RelationshipUpdate_OPERATION_CREATE:
		op = UpdateOperationCreate
	case v1.RelationshipUpdate_OPERATION_TOUCH:
		op = UpdateOperationTouch
	case v1.RelationshipUpdate_OPERATION_DELETE:
		op = UpdateOperationDelete
	}
	return Update{
		Operation:    op,
		Relationship: FromProto(pu.GetRelationship()),
	}
}

// Txn is a transaction builder for batching relationship writes.
type Txn struct {
	// V1Updates exposes the underlying proto updates for advanced use cases.
	V1Updates       []*v1.RelationshipUpdate
	preconditions   []*v1.Precondition
}

// Preconditions returns the preconditions added to this transaction.
func (t *Txn) Preconditions() []*v1.Precondition {
	return t.preconditions
}

// Create adds a relationship create to the transaction. Fails if the
// relationship already exists.
func (t *Txn) Create(r Relationship) {
	t.V1Updates = append(t.V1Updates, &v1.RelationshipUpdate{
		Operation:    v1.RelationshipUpdate_OPERATION_CREATE,
		Relationship: r.ToProto(),
	})
}

// Touch adds a relationship touch to the transaction. Creates or updates
// the relationship.
func (t *Txn) Touch(r Relationship) {
	t.V1Updates = append(t.V1Updates, &v1.RelationshipUpdate{
		Operation:    v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: r.ToProto(),
	})
}

// Delete adds a relationship delete to the transaction.
func (t *Txn) Delete(r Relationship) {
	t.V1Updates = append(t.V1Updates, &v1.RelationshipUpdate{
		Operation:    v1.RelationshipUpdate_OPERATION_DELETE,
		Relationship: r.ToProto(),
	})
}

// MustNotMatch adds a precondition that no relationships match the given filter.
func (t *Txn) MustNotMatch(f Filter) {
	t.preconditions = append(t.preconditions, &v1.Precondition{
		Operation: v1.Precondition_OPERATION_MUST_NOT_MATCH,
		Filter:    f.ToProto(),
	})
}

// MustMatch adds a precondition that at least one relationship matches the given filter.
func (t *Txn) MustMatch(f Filter) {
	t.preconditions = append(t.preconditions, &v1.Precondition{
		Operation: v1.Precondition_OPERATION_MUST_MATCH,
		Filter:    f.ToProto(),
	})
}
