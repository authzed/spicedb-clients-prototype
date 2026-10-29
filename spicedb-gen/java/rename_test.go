package java

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collidingSchema declares `definition subject`, whose ref type SubjectRef collides with the native
// SubjectRef, and a caveat calling a function outside SpiceDB's default CEL environment (caveat
// expressions are opaque to generation).
const collidingSchema = `
caveat custom_fn(request map<any>) {
	customLookup(request).allowed
}

definition subject {}

definition resource {
	relation viewer: subject | subject with custom_fn
	relation owner: subject
	permission view = viewer + owner
}`

func TestRenameResolvesDefinitionCollisions(t *testing.T) {
	s, err := schema.ParseString(collidingSchema)
	require.NoError(t, err, "a caveat calling an undeclared function must parse")
	require.Len(t, s.Caveats, 1)

	_, err = (&Generator{}).Generate(s, map[string]string{"package": "p"})
	require.ErrorContains(t, err, "SubjectRef")
	require.ErrorContains(t, err, "--java.rename", "the collision error must say how to keep the schema")

	files, err := (&Generator{}).Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.renamed", "rename": "subject=SubjectDef"})
	require.NoError(t, err)
	out := string(files[0].Content)
	assert.Contains(t, out, "public static SubjectDefRef SubjectDef(String id) {")
	assert.Contains(t, out, `return "subject";`, "the wire name stays the schema name")
	assert.Contains(t, out, "public SubjectDefCustomFn withCustomFn(CustomFnContext ctx) {")
	assert.Contains(t, out, "public RelationshipKey viewerKey(ResourceViewerKeySubject subject) {")
	assert.NotContains(t, out, "ownerKey(", "a relation without caveats or expiration needs no key")

	for _, bad := range []string{"subject", "subject=lower", "subject=Bad-Name", "subject=", "nope=Nope"} {
		_, err := (&Generator{}).Generate(s, map[string]string{"package": "p", "rename": bad})
		assert.Error(t, err, bad)
	}
	_, err = (&Generator{}).Generate(s, map[string]string{"package": "p", "bogus": "x"})
	assert.ErrorContains(t, err, "--java.bogus")

	if testing.Short() {
		return
	}
	// The renamed output compiles against the real native client and is google-java-format clean.
	dir := filepath.Join("..", "testdata", "java", "src", "main", "java", "com", "authzed", "spicedb", "gen", "renamed")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Permissions.java"), files[0].Content, 0o644))
	cmd := exec.Command("./gradlew", "compileJava", "spotlessCheck", "--console=plain")
	cmd.Dir = filepath.Join("..", "testdata", "java")
	buildOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(buildOut))
}
