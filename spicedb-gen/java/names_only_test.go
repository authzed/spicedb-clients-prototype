package java

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func generateNamesOnly(t *testing.T, zed string, opts map[string]string) (string, error) {
	t.Helper()
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	if err != nil {
		return "", err
	}
	require.Len(t, files, 1)
	assert.Equal(t, "Permissions.java", files[0].Path)
	return string(files[0].Content), nil
}

func TestNamesOnly(t *testing.T) {
	zed := "caveat c(x int) { x > 0 }\ndefinition user {}\ndefinition doc {\n\trelation viewer: user | user with c\n\tpermission can_view = viewer\n}\n"
	out, err := generateNamesOnly(t, zed, map[string]string{"package": "com.example.api", "names-only": "true"})
	require.NoError(t, err)

	imports := regexp.MustCompile(`(?m)^import .*;$`).FindAllString(out, -1)
	assert.Equal(t, []string{"import java.util.List;"}, imports, "names-only output needs nothing beyond the JDK")
	assert.NotContains(t, out, "com.authzed")
	for _, want := range []string{
		"package com.example.api;",
		"public final class Permissions {",
		"public static final class DocRelations {",
		`public static final String VIEWER = "viewer";`,
		"public static final class DocPermissions {",
		`public static final String CAN_VIEW = "can_view";`,
		"public static final class Definitions {",
		`public static final String DOC = "doc";`,
		"public static final String SCHEMA",
		`return List.of("user", "doc");`,
	} {
		assert.Contains(t, out, want)
	}
	for _, absent := range []string{"TypedClient", "DocRef", "CContext", "definitionByName", "PermissionRef"} {
		assert.NotContains(t, out, absent)
	}

	// Renames still apply, so identifiers match the full client's.
	out, err = generateNamesOnly(t, "definition subject {}\ndefinition doc {\n\trelation viewer: subject\n}",
		map[string]string{"package": "p", "names-only": "true", "rename": "subject=SubjectDef"})
	require.NoError(t, err)
	assert.Contains(t, out, "public static final class SubjectDefRelations {")

	out, err = generateNamesOnly(t, zed, map[string]string{"package": "p", "names-only": "false"})
	require.NoError(t, err)
	assert.Contains(t, out, "TypedClient")

	_, err = generateNamesOnly(t, zed, map[string]string{"package": "p", "names-only": "nope"})
	require.ErrorContains(t, err, "--java.names-only")
	_, err = generateNamesOnly(t, zed, map[string]string{"names-only": "true"})
	require.ErrorContains(t, err, "--java.package")
}
