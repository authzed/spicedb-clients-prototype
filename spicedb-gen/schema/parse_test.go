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
