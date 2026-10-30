package golang

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

// emptyGoConstruct matches a switch, select, if, for or else whose body is
// empty, which a schema without wildcards, caveats or expiration could leave
// behind if a template ranged over an empty list inside one.
var emptyGoConstruct = regexp.MustCompile(`(?m)(?:\b(?:switch|select|if|for)\b[^{\n]*|\belse\s*)\{\s*\}`)

func TestMinimalSchemaHasNoEmptyConstructs(t *testing.T) {
	out, err := generate(t, minimalSchema, nil)
	require.NoError(t, err)
	assert.Empty(t, emptyGoConstruct.FindAllString(out, -1))

	if testing.Short() {
		return
	}
	// The output passes go vet and staticcheck with every check enabled, as
	// mage goTypeErrors holds the sample schema's output to.
	dir, err := os.MkdirTemp("../testdata/go", "zz_minimal_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "permissions.gen.go"), []byte(out), 0o644))
	for _, args := range [][]string{
		{"vet", "./" + filepath.Base(dir)},
		{"run", "honnef.co/go/tools/cmd/staticcheck@v0.7.0", "-checks", "all", "./" + filepath.Base(dir)},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = "../testdata/go"
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
		toolOut, err := cmd.CombinedOutput()
		require.NoError(t, err, "go %s: %s", args[0], toolOut)
	}
}
