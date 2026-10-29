package python

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

// importRe matches every import statement; names-only output may import only
// __future__ and typing.
var importRe = regexp.MustCompile(`(?m)^\s*(?:from\s+(\S+)\s+import|import\s+(\S+))`)

func TestNamesOnly(t *testing.T) {
	zed := "caveat c(x int) { x > 0 }\ndefinition user {}\ndefinition doc {\n\trelation viewer: user | user with c\n\tpermission can_view = viewer\n}\n"
	files, err := (&Generator{}).Generate(parse(t, zed), map[string]string{"names-only": "true"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "names.py", files[0].Path)
	out := string(files[0].Content)

	for _, m := range importRe.FindAllStringSubmatch(out, -1) {
		mod := m[1] + m[2]
		assert.Contains(t, []string{"__future__", "typing"}, mod, "names-only output imports the standard library alone")
	}
	assert.NotContains(t, out, "import spicedb")
	assert.NotContains(t, out, "from spicedb")
	for _, want := range []string{
		"class DocRelations:\n",
		`    VIEWER: Final = "viewer"`,
		`    CAN_VIEW: Final = "can_view"`,
		`DOC_PERMISSION_NAMES: Final[tuple[str, ...]] = ("can_view",)`,
		"class DefinitionNames:\n",
		"DEFINITION_NAMES: Final[tuple[str, ...]] = (\n",
		"SCHEMA_TEXT: Final[str] = (\n",
	} {
		assert.Contains(t, out, want)
	}
	for _, absent := range []string{"TypedClient", "class Doc:", "CContext", "definition_by_name", "PermissionRef"} {
		assert.NotContains(t, out, absent)
	}

	// --python.module names the file; renames still apply, so identifiers
	// match the full output's.
	files, err = (&Generator{}).Generate(parse(t, zed), map[string]string{
		"names-only": "true", "module": "apinames", "rename": "doc=Document",
	})
	require.NoError(t, err)
	assert.Equal(t, "apinames.py", files[0].Path)
	assert.Contains(t, string(files[0].Content), "class DocumentRelations:\n")

	files, err = (&Generator{}).Generate(parse(t, zed), map[string]string{"names-only": "false"})
	require.NoError(t, err)
	assert.Len(t, files, 3)

	_, err = (&Generator{}).Generate(parse(t, zed), map[string]string{"names-only": "maybe"})
	require.ErrorContains(t, err, "--python.names-only")

	// Collisions still fail generation.
	_, err = (&Generator{}).Generate(parse(t, "definition subject {}\n"), map[string]string{"names-only": "true"})
	require.Error(t, err)
}

// testdata/python-names/names.py is names-only output that `mage
// pythonTypeErrors` runs with no site-packages; it must match what the
// generator emits today.
func TestNamesOnlyFixtureIsCurrent(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"names-only": "true"})
	require.NoError(t, err)
	want, err := os.ReadFile("../testdata/python-names/names.py")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(files[0].Content),
		"regenerate: spicedb-gen --schema testdata/sample.zed --lang python --python.names-only --out testdata/python-names")
}
