package typescript

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

var importRe = regexp.MustCompile(`(?m)^\s*(import|export\s+\*|export\s+\{[^}]*\}\s+from)\b`)

func TestNamesOnly(t *testing.T) {
	zed := "caveat c(x int) { x > 0 }\ndefinition user {}\ndefinition doc {\n\trelation viewer: user | user with c\n\tpermission can_view = viewer\n}\n"
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"names-only": "true"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "names.gen.ts", files[0].Path)
	out := string(files[0].Content)

	assert.False(t, importRe.MatchString(out), "names-only output imports nothing, so it never depends on the client")
	assert.NotContains(t, out, "@spicedb/")
	for _, want := range []string{
		`export type DocRelationName = "viewer";`,
		`export type DocPermissionName = "can_view";`,
		`canView: "can_view",`,
		`export const DocRelationNames: readonly DocRelationName[] = Object.freeze(["viewer"]);`,
		`export type DefinitionName = "user" | "doc";`,
		`export const DefinitionNames: readonly DefinitionName[] = Object.freeze(["user", "doc"]);`,
		`export const SchemaText: string = ` + jsString(zed) + `;`,
	} {
		assert.Contains(t, out, want)
	}
	for _, absent := range []string{"TypedClient", "DocRef", "CContext", "definitionByName", "Permission<"} {
		assert.NotContains(t, out, absent)
	}

	// Renames still apply, so identifiers match the full client's.
	out, err = generateFrom(t, collidingSchema(t), map[string]string{"names-only": "true", "rename": "subject=SubjectDef"})
	require.NoError(t, err)
	assert.Contains(t, out, "export const SubjectDefRelations")

	out, err = generateFrom(t, zed, map[string]string{"names-only": "false"})
	require.NoError(t, err)
	assert.Contains(t, out, "export class TypedClient")

	_, err = generateFrom(t, zed, map[string]string{"names-only": "maybe"})
	require.ErrorContains(t, err, "--typescript.names-only")
	_, err = generateFrom(t, zed, map[string]string{"names-only": "true", "client-module": "x"})
	require.ErrorContains(t, err, "--typescript.client-module")
}

// testdata/typescript-names holds names-only output that `mage
// typeScriptTypeErrors` type-checks with no packages installed; it must match
// what the generator emits today.
func TestNamesOnlyFixtureIsCurrent(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"names-only": "true"})
	require.NoError(t, err)
	want, err := os.ReadFile("../testdata/typescript-names/names.gen.ts")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(files[0].Content),
		"regenerate: spicedb-gen --schema testdata/sample.zed --lang typescript --typescript.names-only --out testdata/typescript-names/names.gen.ts")
}
