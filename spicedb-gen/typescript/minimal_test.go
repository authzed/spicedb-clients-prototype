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
	typeCheckStrictly(t, "minimal", out)
}

// variantSchema has every subject ref variant: caveated, expiring, both
// (whose withExpiration carries the caveat context), and a caveated wildcard.
const variantSchema = `
use expiration

caveat ip_range(cidr string) {
	cidr == "0.0.0.0/0"
}

definition user {}

definition document {
	relation viewer: user with ip_range | user with ip_range and expiration | user:* | user:* with ip_range
	relation editor: user with expiration
}`

// Each maker declares only the parameters it uses: an unused parameter
// fails noUnusedParameters, and lint rules callers run on generated code.
func TestRefMakersDeclareOnlyUsedParameters(t *testing.T) {
	out, err := generateFrom(t, variantSchema, nil)
	require.NoError(t, err)
	for _, want := range []string{
		"function makeUserIpRangeRef(id: string, ctx: JSONObject): UserIpRangeRef {",
		"function makeUserIpRangeExpiringRef(id: string, ctx: JSONObject, at: Date): UserIpRangeExpiringRef {",
		"function makeUserExpiringRef(id: string, at: Date): UserExpiringRef {",
		"function makeUserWildcardRef(): UserWildcardRef {",
		"function makeUserWildcardIpRangeRef(ctx: JSONObject): UserWildcardIpRangeRef {",
		"withExpiration: (expiresAt: Date) => makeUserIpRangeExpiringRef(id, ctx, expiresAt),",
		"withExpiration: (expiresAt: Date) => makeUserExpiringRef(id, expiresAt),",
	} {
		assert.Contains(t, out, want)
	}
	assert.NotContains(t, out, "ctx?: JSONObject")
	assert.NotContains(t, out, "at?: Date")
	// Options are split without destructuring into unused variables, which
	// trips no-unused-vars unless a caller's lint sets ignoreRestSiblings.
	assert.NotContains(t, out, "const { signal, timeoutMs")
	typeCheckStrictly(t, "variants", out)
}

// typeCheckStrictly type-checks generated output under the fixture's strict
// options, including noUnusedLocals and noUnusedParameters, which report an
// unused import, helper or parameter. Short mode skips it.
func typeCheckStrictly(t *testing.T, name, out string) {
	t.Helper()
	if testing.Short() {
		return
	}
	tsc := filepath.Join("..", "testdata", "typescript", "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skip("run pnpm install in testdata/typescript to type-check the output")
	}
	dir, err := os.MkdirTemp("../testdata/typescript", "zz_"+name+"_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "permissions.ts"), []byte(out), 0o644))
	tsconfig := `{
    "extends": "../tsconfig.json",
    "compilerOptions": { "noUnusedLocals": true, "noUnusedParameters": true },
    "include": ["*.ts"]
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(tsconfig), 0o644))
	cmd := exec.Command("node_modules/.bin/tsc", "--noEmit", "-p", filepath.Base(dir))
	cmd.Dir = "../testdata/typescript"
	tscOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(tscOut))
}
