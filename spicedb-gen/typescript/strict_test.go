package typescript

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The fixtures type-check generated output (full and names-only) under the
// strictest options callers commonly enable; mage typeScriptTypeErrors and the
// integration test run tsc with these configs. Dropping an option would let a
// strictness regression in generated code ship unnoticed.
func TestFixturesTypeCheckStrictly(t *testing.T) {
	for _, path := range []string{"../testdata/typescript/tsconfig.json", "../testdata/typescript-names/tsconfig.json"} {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var cfg struct {
			CompilerOptions map[string]any `json:"compilerOptions"`
		}
		require.NoError(t, json.Unmarshal(raw, &cfg), path)
		for _, opt := range []string{"strict", "noImplicitAny", "exactOptionalPropertyTypes", "noUncheckedIndexedAccess"} {
			require.Equal(t, true, cfg.CompilerOptions[opt], "%s must enable %s", path, opt)
		}
	}
}
