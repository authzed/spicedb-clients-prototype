package typescript

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

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

// emptyTSConstructs match what ESLint's no-empty reports (an empty switch, or
// an empty block after if, for, while, catch, else, try or finally) and an
// empty type union or intersection.
var emptyTSConstructs = []*regexp.Regexp{
	regexp.MustCompile(`(?m)\b(?:switch|if|for|while|catch)\s*\([^\n]*\)\s*\{\s*\}`),
	regexp.MustCompile(`\b(?:else|try|finally)\s*\{\s*\}`),
	regexp.MustCompile(`(?m)=\s*[|&]?\s*;|[|&]\s*;`),
}

func TestMinimalSchemaHasNoEmptyConstructs(t *testing.T) {
	out, err := generateFrom(t, minimalSchema, nil)
	require.NoError(t, err)
	for _, re := range emptyTSConstructs {
		assert.Empty(t, re.FindAllString(out, -1), re.String())
	}
	assert.NotContains(t, out, "switch (type)", "parseSubject needs no switch without wildcard types")

	if testing.Short() {
		return
	}
	// The output type-checks under the fixture's strict options plus
	// noUnusedLocals, which reports an unused import.
	tsc := filepath.Join("..", "testdata", "typescript", "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skip("run pnpm install in testdata/typescript to type-check the output")
	}
	dir, err := os.MkdirTemp("../testdata/typescript", "zz_minimal_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "permissions.ts"), []byte(out), 0o644))
	tsconfig := `{
    "extends": "../tsconfig.json",
    "compilerOptions": { "noUnusedLocals": true },
    "include": ["*.ts"]
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644))
	cmd := exec.Command("node_modules/.bin/tsc", "--noEmit", "-p", filepath.Base(dir))
	cmd.Dir = "../testdata/typescript"
	tscOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(tscOut))
}
