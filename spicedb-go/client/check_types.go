package client

import (
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// CheckResult is the outcome of a permission check. Permissionship carries the
// server's three-valued answer; a Conditional result means the server needed
// caveat context that was not supplied and is NOT a grant.
type CheckResult struct {
	// Permissionship is the server's answer. Prefer HasPermission() for the
	// common case.
	Permissionship Permissionship
	// MissingContext lists the caveat context keys the server needed and did
	// not receive. Empty unless Permissionship is Conditional.
	MissingContext []string
	// CheckedAt is the revision this check was evaluated at. Thread it into
	// consistency.AtLeast to make a later read observe this one.
	CheckedAt string
}

// HasPermission reports whether the subject has the permission outright. It is
// false for a Conditional result: the server could not evaluate the caveat, so
// treating it as a grant would authorize on an unevaluated condition.
func (r CheckResult) HasPermission() bool {
	return r.Permissionship == PermissionshipHasPermission
}

// checkPermissionshipFromProto maps the proto CheckPermissionResponse
// Permissionship enum (which, unlike LookupPermissionship, has a
// NO_PERMISSION value) to its native equivalent. Unrecognized values map to
// PermissionshipUnspecified.
func checkPermissionshipFromProto(v v1.CheckPermissionResponse_Permissionship) Permissionship {
	switch v {
	case v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION:
		return PermissionshipHasPermission
	case v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION:
		return PermissionshipConditionalPermission
	case v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION:
		return PermissionshipNoPermission
	default:
		return PermissionshipUnspecified
	}
}

// checkResultFromProto maps a *v1.CheckPermissionResponse (the response from
// a single, non-bulk CheckPermission RPC) to a native CheckResult. The
// response's own checked_at is used directly.
func checkResultFromProto(resp *v1.CheckPermissionResponse) CheckResult {
	return CheckResult{
		Permissionship: checkPermissionshipFromProto(resp.GetPermissionship()),
		MissingContext: resp.GetPartialCaveatInfo().GetMissingRequiredContext(),
		CheckedAt:      resp.GetCheckedAt().GetToken(),
	}
}

// checkResultFromBulkItem maps a *v1.CheckBulkPermissionsResponseItem (one
// pair's successful result from a CheckBulkPermissions call) to a native
// CheckResult. CheckBulkPermissionsResponseItem carries the same
// permissionship and partial-caveat-info fields as CheckPermissionResponse,
// but has no per-item checked_at of its own — the token lives once on the
// enclosing CheckBulkPermissionsResponse and applies to every pair in it, so
// callers pass it in as responseCheckedAt to propagate onto each item.
func checkResultFromBulkItem(item *v1.CheckBulkPermissionsResponseItem, responseCheckedAt string) CheckResult {
	return CheckResult{
		Permissionship: checkPermissionshipFromProto(item.GetPermissionship()),
		MissingContext: item.GetPartialCaveatInfo().GetMissingRequiredContext(),
		CheckedAt:      responseCheckedAt,
	}
}
