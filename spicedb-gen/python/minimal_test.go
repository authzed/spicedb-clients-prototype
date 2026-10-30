package python

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

// emptyPythonConstructs match an empty subscript (Literal[], Union[]), an
// alias with nothing after the equals sign, and a dangling union bar.
var emptyPythonConstructs = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:Literal|Union|tuple|frozenset)\[\s*\]`),
	regexp.MustCompile(`(?m)TypeAlias\s*=\s*\|?\s*$`),
	regexp.MustCompile(`(?m)\|\s*$|=\s*\|`),
}

func TestMinimalSchemaHasNoEmptyConstructs(t *testing.T) {
	files := generate(t, parse(t, minimalSchema), nil)
	for name, content := range files {
		for _, re := range emptyPythonConstructs {
			assert.Empty(t, re.FindAllString(content, -1), "%s: %s", name, re.String())
		}
	}

	if testing.Short() {
		return
	}
	// The output passes mypy --strict, pyright and ruff's unused-import and
	// syntax checks, unused-argument check (ARG) and line-length check
	// (E501), against the real native client.
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
	dir, err := os.MkdirTemp("../testdata/python", "zz_minimal_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "__init__.py"), nil, 0o644))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	pkg := filepath.Base(dir)
	for _, args := range [][]string{
		{"run", "mypy", "--strict", "-p", pkg},
		{"run", "pyright", pkg},
		{"run", "ruff", "check", "--isolated", "--line-length", "88", "--select", "E4,E7,E9,F,ARG,E501", pkg},
	} {
		cmd := exec.Command("uv", args...)
		cmd.Dir = "../testdata/python"
		toolOut, err := cmd.CombinedOutput()
		require.NoError(t, err, "uv %v: %s", args, toolOut)
	}
}
