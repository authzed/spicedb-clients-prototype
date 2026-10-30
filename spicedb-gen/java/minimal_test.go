package java

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalSchema has one relation and nothing optional: no permissions, no
// wildcards, no caveats, no expiration and no subject sets. Every
// schema-driven list the generator ranges over is empty or has one entry.
const minimalSchema = `
definition user {}

definition document {
	relation viewer: user
}`

// emptyJavaConstructs match an empty switch, an empty block after if, for,
// while, catch, else, try or finally, and a sealed type with no permitted
// subtypes.
var emptyJavaConstructs = []*regexp.Regexp{
	regexp.MustCompile(`(?m)\b(?:switch|if|for|while|catch)\s*\([^\n]*\)\s*\{\s*\}`),
	regexp.MustCompile(`\b(?:else|try|finally)\s*\{\s*\}`),
	regexp.MustCompile(`\bpermits\s*\{`),
}

func TestMinimalSchemaHasNoEmptyConstructs(t *testing.T) {
	s, err := schema.ParseString(minimalSchema)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.minimal"})
	require.NoError(t, err)
	out := string(files[0].Content)
	for _, re := range emptyJavaConstructs {
		assert.Empty(t, re.FindAllString(out, -1), re.String())
	}

	if testing.Short() {
		return
	}
	// The output compiles under -Xlint:all -Werror (compileJava's flags)
	// against the real native client and is google-java-format clean.
	dir := filepath.Join("..", "testdata", "java", "src", "main", "java", "com", "authzed", "spicedb", "gen", "minimal")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Permissions.java"), files[0].Content, 0o644))
	cmd := exec.Command("./gradlew", "compileJava", "spotlessCheck", "--console=plain")
	cmd.Dir = filepath.Join("..", "testdata", "java")
	buildOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(buildOut))
}
