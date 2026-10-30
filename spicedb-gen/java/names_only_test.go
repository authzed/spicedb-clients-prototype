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
	path, out, err := generateNamed(t, zed, opts)
	if err == nil {
		want := "PermissionNames.java"
		if opts["names-only"] != "true" {
			want = "Permissions.java"
		}
		assert.Equal(t, want, path)
	}
	return out, err
}

func generateNamed(t *testing.T, zed string, opts map[string]string) (string, string, error) {
	t.Helper()
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	if err != nil {
		return "", "", err
	}
	require.Len(t, files, 1)
	return files[0].Path, string(files[0].Content), nil
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
		// Not Permissions, the full client's class: a project can use both.
		"public final class PermissionNames {",
		"private PermissionNames() {}",
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

func TestNamesOnlyClassName(t *testing.T) {
	zed := "definition user {}\ndefinition doc {\n\trelation viewer: user\n}\n"
	path, out, err := generateNamed(t, zed, map[string]string{"package": "p", "names-only": "true", "names-class": "SchemaNames"})
	require.NoError(t, err)
	assert.Equal(t, "SchemaNames.java", path)
	assert.Contains(t, out, "public final class SchemaNames {")
	assert.Contains(t, out, "private SchemaNames() {}")

	// The full client keeps Permissions; the option applies only to names-only output.
	_, _, err = generateNamed(t, zed, map[string]string{"package": "p", "names-class": "SchemaNames"})
	require.ErrorContains(t, err, "--java.names-only")
	for _, bad := range []string{"schemaNames", "", "Bad-Name"} {
		_, _, err = generateNamed(t, zed, map[string]string{"package": "p", "names-only": "true", "names-class": bad})
		require.ErrorContains(t, err, "--java.names-class", bad)
	}
	// A class name a nested class already uses fails, naming both.
	for _, clash := range []string{"Definitions", "DocRelations"} {
		_, _, err = generateNamed(t, zed, map[string]string{"package": "p", "names-only": "true", "names-class": clash})
		require.ErrorContains(t, err, "collides", clash)
	}
}
