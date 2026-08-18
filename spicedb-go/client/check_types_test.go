package client

import (
	"reflect"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

func TestCheckResultHasPermissionOnlyForHasPermission(t *testing.T) {
	cases := []struct {
		ship Permissionship
		want bool
	}{
		{PermissionshipHasPermission, true},
		{PermissionshipConditionalPermission, false},
		{PermissionshipNoPermission, false},
		{PermissionshipUnspecified, false},
	}
	for _, c := range cases {
		got := CheckResult{Permissionship: c.ship}.HasPermission()
		if got != c.want {
			t.Errorf("Permissionship %v: HasPermission() = %v, want %v", c.ship, got, c.want)
		}
	}
}

func TestCheckResultCarriesMissingContext(t *testing.T) {
	r := checkResultFromProto(&v1.CheckPermissionResponse{
		Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
		PartialCaveatInfo: &v1.PartialCaveatInfo{
			MissingRequiredContext: []string{"now", "ip"},
		},
		CheckedAt: &v1.ZedToken{Token: "tok-1"},
	})
	if r.Permissionship != PermissionshipConditionalPermission {
		t.Errorf("got %v, want Conditional", r.Permissionship)
	}
	if !reflect.DeepEqual(r.MissingContext, []string{"now", "ip"}) {
		t.Errorf("MissingContext = %v, want [now ip]", r.MissingContext)
	}
	if r.CheckedAt != "tok-1" {
		t.Errorf("CheckedAt = %q, want tok-1", r.CheckedAt)
	}
	if r.HasPermission() {
		t.Error("conditional result must not report HasPermission")
	}
}

// TestCheckResultFromBulkItem_PropagatesResponseLevelToken proves that the
// bulk-item variant of the mapper — used by Check/CheckOne/CheckIter, which
// call CheckBulkPermissions exclusively — carries the SAME checked_at token
// into every item, since CheckBulkPermissionsResponseItem has no per-item
// checked_at field of its own (only the response as a whole does).
func TestCheckResultFromBulkItem_PropagatesResponseLevelToken(t *testing.T) {
	item := &v1.CheckBulkPermissionsResponseItem{
		Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
	}
	r := checkResultFromBulkItem(item, "response-tok")
	if r.Permissionship != PermissionshipHasPermission {
		t.Errorf("got %v, want HasPermission", r.Permissionship)
	}
	if r.CheckedAt != "response-tok" {
		t.Errorf("CheckedAt = %q, want response-tok", r.CheckedAt)
	}
	if len(r.MissingContext) != 0 {
		t.Errorf("MissingContext = %v, want empty", r.MissingContext)
	}
}

func TestCheckResultFromBulkItem_MapsNoPermissionAndConditional(t *testing.T) {
	cases := []struct {
		name string
		in   v1.CheckPermissionResponse_Permissionship
		want Permissionship
	}{
		{"no permission", v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION, PermissionshipNoPermission},
		{"has permission", v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, PermissionshipHasPermission},
		{"conditional", v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION, PermissionshipConditionalPermission},
		{"unspecified", v1.CheckPermissionResponse_PERMISSIONSHIP_UNSPECIFIED, PermissionshipUnspecified},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			item := &v1.CheckBulkPermissionsResponseItem{Permissionship: c.in}
			r := checkResultFromBulkItem(item, "tok")
			if r.Permissionship != c.want {
				t.Errorf("got %v, want %v", r.Permissionship, c.want)
			}
		})
	}
}
