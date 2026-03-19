package schema_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func testdataPath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

func TestParseFile(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)
	require.Len(t, s.Definitions, 3)

	// Verify definition names.
	assert.Equal(t, "user", s.Definitions[0].Name)
	assert.Equal(t, "team", s.Definitions[1].Name)
	assert.Equal(t, "document", s.Definitions[2].Name)
}

func TestUserDefinition(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	user := s.Definitions[0]
	assert.Empty(t, user.Relations)
	assert.Empty(t, user.Permissions)
}

func TestTeamDefinition(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	team := s.Definitions[1]
	require.Len(t, team.Relations, 1)
	assert.Equal(t, "member", team.Relations[0].Name)
	require.Len(t, team.Relations[0].AllowedSubjects, 2)

	subjects := team.Relations[0].AllowedSubjects
	assert.Contains(t, subjects, schema.SubjectType{Definition: "user"})
	assert.Contains(t, subjects, schema.SubjectType{Definition: "team", Relation: "member"})
}

func TestDocumentRelations(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	require.Len(t, doc.Relations, 3)
	assert.Equal(t, "viewer", doc.Relations[0].Name)
	assert.Equal(t, "editor", doc.Relations[1].Name)
	assert.Equal(t, "owner", doc.Relations[2].Name)

	// viewer now has 4 allowed subject types: user, user with ip_range, user with time_window, team#member
	require.Len(t, doc.Relations[0].AllowedSubjects, 4)
}

func TestDocumentPermissions(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	require.Len(t, doc.Permissions, 3)
	assert.Equal(t, "view", doc.Permissions[0].Name)
	assert.Equal(t, "edit", doc.Permissions[1].Name)
	assert.Equal(t, "delete", doc.Permissions[2].Name)
}

func TestViewPermissionReachableSubjects(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	viewPerm := doc.Permissions[0]

	// view = viewer + editor + owner
	// viewer allows: user, team#member
	// editor allows: user
	// owner allows: user
	// So reachable: user, team#member
	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "user"})
	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "team", Relation: "member"})
}

func TestEditPermissionReachableSubjects(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	editPerm := doc.Permissions[1]

	// edit = editor + owner
	// Both editor and owner allow only: user
	assert.Contains(t, editPerm.ReachableSubjects, schema.SubjectType{Definition: "user"})
}

func TestDeletePermissionReachableSubjects(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	deletePerm := doc.Permissions[2]

	// delete = owner, which allows only: user
	require.Len(t, deletePerm.ReachableSubjects, 1)
	assert.Equal(t, schema.SubjectType{Definition: "user"}, deletePerm.ReachableSubjects[0])
}

func TestInvalidSchema(t *testing.T) {
	_, err := schema.ParseString("this is not valid schema syntax {{{")
	assert.Error(t, err)
}

func TestEmptySchema(t *testing.T) {
	s, err := schema.ParseString("")
	require.NoError(t, err)
	assert.Empty(t, s.Definitions)
}

func TestCaveatDefinitions(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)
	require.Len(t, s.Caveats, 2)

	caveatsByName := map[string]schema.CaveatDefinition{}
	for _, c := range s.Caveats {
		caveatsByName[c.Name] = c
	}

	ipRange := caveatsByName["ip_range"]
	require.Len(t, ipRange.Params, 1)
	assert.Equal(t, "allowed_cidr", ipRange.Params[0].Name)
	assert.Equal(t, "string", ipRange.Params[0].Type)

	timeWindow := caveatsByName["time_window"]
	require.Len(t, timeWindow.Params, 2)
	paramNames := []string{timeWindow.Params[0].Name, timeWindow.Params[1].Name}
	assert.Contains(t, paramNames, "start")
	assert.Contains(t, paramNames, "end")
}

func TestCaveatOnSubjectType(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	viewerRel := doc.Relations[0]
	require.Equal(t, "viewer", viewerRel.Name)

	// Find the caveated user entries.
	var bareUser, ipRangeUser, timeWindowUser *schema.SubjectType
	var teamMember *schema.SubjectType
	for i := range viewerRel.AllowedSubjects {
		st := &viewerRel.AllowedSubjects[i]
		switch {
		case st.Definition == "user" && st.CaveatName == "":
			bareUser = st
		case st.Definition == "user" && st.CaveatName == "ip_range":
			ipRangeUser = st
		case st.Definition == "user" && st.CaveatName == "time_window":
			timeWindowUser = st
		case st.Definition == "team" && st.Relation == "member":
			teamMember = st
		}
	}

	require.NotNil(t, bareUser, "expected bare user subject")
	require.NotNil(t, ipRangeUser, "expected user with ip_range caveat")
	require.NotNil(t, timeWindowUser, "expected user with time_window caveat")
	require.NotNil(t, teamMember, "expected team#member subject")

	assert.Equal(t, "ip_range", ipRangeUser.CaveatName)
	assert.Equal(t, "time_window", timeWindowUser.CaveatName)
	assert.Empty(t, teamMember.CaveatName)
	assert.Empty(t, bareUser.CaveatName)
}

func TestViewPermissionReachableSubjectsWithCaveats(t *testing.T) {
	s, err := schema.ParseFile(testdataPath("sample.zed"))
	require.NoError(t, err)

	doc := s.Definitions[2]
	viewPerm := doc.Permissions[0]

	// view = viewer + editor + owner
	// viewer allows: user, user with ip_range, user with time_window, team#member
	// editor allows: user
	// owner allows: user
	// After dedup (with caveat in key): user, user with ip_range, user with time_window, team#member
	require.Len(t, viewPerm.ReachableSubjects, 4)

	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "user"})
	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "user", CaveatName: "ip_range"})
	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "user", CaveatName: "time_window"})
	assert.Contains(t, viewPerm.ReachableSubjects, schema.SubjectType{Definition: "team", Relation: "member"})
}
