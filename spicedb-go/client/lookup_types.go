package client

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// Permissionship indicates whether a check or lookup result reflects a full
// grant, a full denial, or is conditional on caveat context that was not
// fully evaluated by the server. Callers MUST check this before treating a
// result as a full grant — a Conditional result may resolve to false once
// the missing caveat context is supplied.
//
// This type serves both the check surface (CheckResult) and the lookup
// surface (LookupResource, ResolvedSubject). Lookups never yield
// PermissionshipNoPermission: a subject/resource pair that lacks the
// permission is simply absent from a lookup stream rather than being
// yielded with that permissionship. NoPermission only appears on
// CheckResult, where the server is answering a question about one specific
// pair and "no" is itself an answer.
//
// PermissionshipNoPermission was added after HasPermission and
// ConditionalPermission and is deliberately appended at the end of the
// const block (not inserted alongside PermissionshipUnspecified) so the
// iota values of the two pre-existing constants are not renumbered.
type Permissionship int

const (
	PermissionshipUnspecified Permissionship = iota
	PermissionshipHasPermission
	PermissionshipConditionalPermission
	PermissionshipNoPermission
)

// String returns a human-readable name for the Permissionship.
func (p Permissionship) String() string {
	switch p {
	case PermissionshipHasPermission:
		return "HasPermission"
	case PermissionshipConditionalPermission:
		return "ConditionalPermission"
	case PermissionshipNoPermission:
		return "NoPermission"
	default:
		return "Unspecified"
	}
}

// PartialCaveatInfo lists caveat context that was missing to fully evaluate a
// conditional result.
type PartialCaveatInfo struct {
	MissingRequiredContext []string
}

// LookupResource is one result from LookupResources.
type LookupResource struct {
	ResourceID     string
	Permissionship Permissionship
	PartialCaveat  *PartialCaveatInfo // non-nil when Permissionship is Conditional
	// LookedUpAt is the revision this result was computed at. It is identical
	// for every item yielded by a single LookupResources call — it is a
	// property of the call, not of the individual resource.
	LookedUpAt string
}

// ResolvedSubject is a subject resolved by LookupSubjects — either the
// matched subject, or (when found in LookupSubject.ExcludedSubjects) a
// subject excluded from a wildcard match.
type ResolvedSubject struct {
	SubjectID      string
	Permissionship Permissionship
	PartialCaveat  *PartialCaveatInfo
}

// LookupSubject is one result from LookupSubjects. When Subject.SubjectID is
// the wildcard "*", ExcludedSubjects lists subjects excluded from that
// wildcard grant — callers MUST treat those subjects as NOT having the
// permission, even though the wildcard would otherwise suggest they do.
type LookupSubject struct {
	Subject          ResolvedSubject
	ExcludedSubjects []ResolvedSubject
	// LookedUpAt is the revision this result was computed at. It is identical
	// for every item yielded by a single LookupSubjects call — it is a
	// property of the call, not of the individual subject.
	LookedUpAt string
}

// permissionshipFromProto maps the proto LookupPermissionship enum to its
// native equivalent. Unrecognized values map to PermissionshipUnspecified.
func permissionshipFromProto(v v1.LookupPermissionship) Permissionship {
	switch v {
	case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION:
		return PermissionshipHasPermission
	case v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
		return PermissionshipConditionalPermission
	default:
		return PermissionshipUnspecified
	}
}

// partialCaveatFromProto maps a proto PartialCaveatInfo to its native
// equivalent. A nil input maps to nil.
func partialCaveatFromProto(v *v1.PartialCaveatInfo) *PartialCaveatInfo {
	if v == nil {
		return nil
	}
	return &PartialCaveatInfo{MissingRequiredContext: v.GetMissingRequiredContext()}
}

// resolvedSubjectFromProto maps a proto ResolvedSubject to its native
// equivalent. A nil input maps to a zero-value ResolvedSubject (empty
// SubjectID), which callers use as the trigger for falling back to
// deprecated response-level fields.
func resolvedSubjectFromProto(v *v1.ResolvedSubject) ResolvedSubject {
	return ResolvedSubject{
		SubjectID:      v.GetSubjectObjectId(),
		Permissionship: permissionshipFromProto(v.GetPermissionship()),
		PartialCaveat:  partialCaveatFromProto(v.GetPartialCaveatInfo()),
	}
}
