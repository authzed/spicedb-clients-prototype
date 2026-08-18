package client

import (
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

func TestPermissionshipFromProto(t *testing.T) {
	cases := []struct {
		name string
		in   v1.LookupPermissionship
		want Permissionship
	}{
		{"unspecified", v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_UNSPECIFIED, PermissionshipUnspecified},
		{"has", v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, PermissionshipHasPermission},
		{"conditional", v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION, PermissionshipConditionalPermission},
		{"unknown value", v1.LookupPermissionship(99), PermissionshipUnspecified},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, permissionshipFromProto(tc.in))
		})
	}
}

func TestPermissionshipString(t *testing.T) {
	require.Equal(t, "Unspecified", PermissionshipUnspecified.String())
	require.Equal(t, "HasPermission", PermissionshipHasPermission.String())
	require.Equal(t, "ConditionalPermission", PermissionshipConditionalPermission.String())
	require.Equal(t, "Unspecified", Permissionship(99).String())
}

func TestPartialCaveatFromProto_NilIsNilSafe(t *testing.T) {
	require.Nil(t, partialCaveatFromProto(nil))
}

func TestPartialCaveatFromProto_MapsMissingContext(t *testing.T) {
	got := partialCaveatFromProto(&v1.PartialCaveatInfo{
		MissingRequiredContext: []string{"ip_address", "time_of_day"},
	})
	require.NotNil(t, got)
	require.Equal(t, []string{"ip_address", "time_of_day"}, got.MissingRequiredContext)
}

func TestResolvedSubjectFromProto_NilIsZeroValue(t *testing.T) {
	got := resolvedSubjectFromProto(nil)
	require.Equal(t, ResolvedSubject{}, got)
	require.Empty(t, got.SubjectID)
}

func TestResolvedSubjectFromProto_MapsAllFields(t *testing.T) {
	got := resolvedSubjectFromProto(&v1.ResolvedSubject{
		SubjectObjectId: "*",
		Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
		PartialCaveatInfo: &v1.PartialCaveatInfo{
			MissingRequiredContext: []string{"region"},
		},
	})
	require.Equal(t, "*", got.SubjectID)
	require.Equal(t, PermissionshipConditionalPermission, got.Permissionship)
	require.NotNil(t, got.PartialCaveat)
	require.Equal(t, []string{"region"}, got.PartialCaveat.MissingRequiredContext)
}
