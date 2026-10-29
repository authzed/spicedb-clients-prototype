package rel

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ResourceType is shorthand for r.Resource().Type().
func (r Relationship) ResourceType() string { return r.resource.objectType }

// ResourceID is shorthand for r.Resource().ID().
func (r Relationship) ResourceID() string { return r.resource.objectID }

// SubjectType is shorthand for r.Subject().Object().Type().
func (r Relationship) SubjectType() string { return r.subject.object.objectType }

// SubjectID is shorthand for r.Subject().Object().ID().
func (r Relationship) SubjectID() string { return r.subject.object.objectID }

// SubjectRelation is shorthand for r.Subject().Relation(); empty for a direct subject.
func (r Relationship) SubjectRelation() string { return r.subject.relation }

// String formats the object as "type:id".
func (o ObjectRef) String() string { return o.objectType + ":" + o.objectID }

// String formats the subject as "type:id" or "type:id#relation".
func (s SubjectRef) String() string {
	if s.relation == "" {
		return s.object.String()
	}
	return s.object.String() + "#" + s.relation
}

// MustNewObject is NewObject for compile-time constants and tests. It panics
// on invalid input.
func MustNewObject(objectType, objectID string) ObjectRef {
	return must(NewObject(objectType, objectID))
}

// MustNewSubject is NewSubject for compile-time constants and tests. It panics
// on invalid input.
func MustNewSubject(object ObjectRef, relation string) SubjectRef {
	return must(NewSubject(object, relation))
}

// MustNewRelationship is NewRelationship for compile-time constants and tests.
// It panics on invalid input.
func MustNewRelationship(resource ObjectRef, relation string, subject SubjectRef) Relationship {
	return must(NewRelationship(resource, relation, subject))
}

// MustNewFilter is NewFilter for compile-time constants and tests. It panics
// on invalid input.
func MustNewFilter(resourceType string) Filter { return must(NewFilter(resourceType)) }

// MustFromTuple is FromTuple for compile-time constants and tests, e.g.
// MustFromTuple("document:readme#viewer@user:alice"). It panics on invalid input.
func MustFromTuple(tuple string) Relationship { return must(FromTuple(tuple)) }

func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("rel: %v", err))
	}
	return v
}

// Matches reports whether r is selected by f, using SpiceDB's filter
// semantics: resource type, resource ID or ID prefix, relation, subject type,
// subject ID, and the Any/Direct/Named subject relation distinction. Unset
// fields match anything. Caveats and expiration never affect matching.
// Matches does not validate f; a filter the server would reject may still match.
func (f Filter) Matches(r Relationship) bool {
	switch {
	case f.err != nil:
		return false
	case f.resourceType != r.resource.objectType,
		f.resourceID != "" && f.resourceID != r.resource.objectID,
		f.resourceIDPrefix != "" && !strings.HasPrefix(r.resource.objectID, f.resourceIDPrefix),
		f.relation != "" && f.relation != r.relation,
		f.subjectType != "" && f.subjectType != r.subject.object.objectType,
		f.subjectID != "" && f.subjectID != r.subject.object.objectID:
		return false
	}
	switch f.subjectRelation.kind {
	case RelationDirect:
		return r.subject.relation == ""
	case RelationNamed:
		return r.subject.relation == f.subjectRelation.name
	default:
		return true
	}
}

// String formats the subject relation filter as "*" (any), "..." (direct) or
// the relation name.
func (r SubjectRelationFilter) String() string {
	switch r.kind {
	case RelationDirect:
		return "..."
	case RelationNamed:
		return r.name
	default:
		return "*"
	}
}

// String formats the filter in a zed-like form, using "*" for unset fields,
// e.g. "document:*#viewer@user:alice" or "document:doc-prefix%*#*@*".
func (f Filter) String() string {
	if f.err != nil {
		return "<invalid filter: " + f.err.Error() + ">"
	}
	or := func(v string) string {
		if v == "" {
			return "*"
		}
		return v
	}
	id := or(f.resourceID)
	if f.resourceIDPrefix != "" {
		id = f.resourceIDPrefix + "%*"
	}
	s := fmt.Sprintf("%s:%s#%s@%s", f.resourceType, id, or(f.relation), or(f.subjectType))
	if f.subjectType != "" {
		s += ":" + or(f.subjectID)
		if f.subjectRelation.kind != RelationAny {
			s += "#" + f.subjectRelation.String()
		}
	}
	return s
}

// String names the operation, e.g. "TOUCH".
func (o UpdateOperation) String() string {
	switch o {
	case UpdateOperationCreate:
		return "CREATE"
	case UpdateOperationTouch:
		return "TOUCH"
	case UpdateOperationDelete:
		return "DELETE"
	default:
		return "UNSPECIFIED"
	}
}

// String formats the update as "OPERATION(relationship)".
func (u Update) String() string { return u.Operation.String() + "(" + u.Relationship.String() + ")" }

// String names the operation, e.g. "MUST_MATCH".
func (o PreconditionOperation) String() string {
	switch o {
	case PreconditionMustMatch:
		return "MUST_MATCH"
	case PreconditionMustNotMatch:
		return "MUST_NOT_MATCH"
	default:
		return "UNSPECIFIED"
	}
}

// String formats the precondition as "OPERATION(filter)".
func (p Precondition) String() string { return p.Operation.String() + "(" + p.Filter.String() + ")" }

// IsIdempotent reports whether replaying the transaction is safe: it contains
// only touches and no preconditions. This is the rule the client uses to decide
// whether a write may be retried.
func (t Txn) IsIdempotent() bool {
	if len(t.preconditions) > 0 {
		return false
	}
	for _, u := range t.updates {
		if u.Operation != UpdateOperationTouch {
			return false
		}
	}
	return true
}

// Len returns the number of updates in the transaction without copying them.
func (t Txn) Len() int { return len(t.updates) }

// String lists the transaction's preconditions and updates.
func (t Txn) String() string {
	parts := make([]string, 0, len(t.preconditions)+len(t.updates))
	for _, p := range t.preconditions {
		parts = append(parts, p.String())
	}
	for _, u := range t.updates {
		parts = append(parts, u.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

type relationshipJSON struct {
	Resource      string         `json:"resource"`
	Relation      string         `json:"relation"`
	Subject       string         `json:"subject"`
	CaveatName    string         `json:"caveat_name,omitempty"`
	CaveatContext map[string]any `json:"caveat_context,omitempty"`
	Expiration    *time.Time     `json:"expiration,omitempty"`
}

func (r Relationship) jsonValue() relationshipJSON {
	return relationshipJSON{
		Resource: r.resource.String(), Relation: r.relation, Subject: r.subject.String(),
		CaveatName: r.caveatName, CaveatContext: r.CaveatContext(), Expiration: r.Expiration(),
	}
}

// MarshalJSON renders the relationship for logs. It is a diagnostic form, not
// a wire format; there is no UnmarshalJSON.
func (r Relationship) MarshalJSON() ([]byte, error) { return json.Marshal(r.jsonValue()) }

// LogValue renders the relationship for log/slog.
func (r Relationship) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("resource", r.resource.String()),
		slog.String("relation", r.relation),
		slog.String("subject", r.subject.String()),
	}
	if r.caveatName != "" {
		attrs = append(attrs, slog.String("caveat_name", r.caveatName), slog.Any("caveat_context", r.CaveatContext()))
	}
	if r.expiration != nil {
		attrs = append(attrs, slog.Time("expiration", *r.expiration))
	}
	return slog.GroupValue(attrs...)
}

// MarshalJSON renders the update for logs.
func (u Update) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Operation    string           `json:"operation"`
		Relationship relationshipJSON `json:"relationship"`
	}{u.Operation.String(), u.Relationship.jsonValue()})
}

// LogValue renders the update for log/slog.
func (u Update) LogValue() slog.Value {
	return slog.GroupValue(slog.String("operation", u.Operation.String()), slog.Any("relationship", u.Relationship))
}

// MarshalJSON renders the filter for logs as its String form.
func (f Filter) MarshalJSON() ([]byte, error) { return json.Marshal(f.String()) }

// LogValue renders the filter for log/slog as its String form.
func (f Filter) LogValue() slog.Value { return slog.StringValue(f.String()) }

// MarshalJSON renders the precondition for logs.
func (p Precondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Operation string `json:"operation"`
		Filter    string `json:"filter"`
	}{p.Operation.String(), p.Filter.String()})
}

// LogValue renders the precondition for log/slog.
func (p Precondition) LogValue() slog.Value {
	return slog.GroupValue(slog.String("operation", p.Operation.String()), slog.String("filter", p.Filter.String()))
}
