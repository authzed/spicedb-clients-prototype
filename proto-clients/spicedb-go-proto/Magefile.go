//go:build mage

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/authzed/spicedb-clients/internal/clauderun"
	"github.com/authzed/spicedb-clients/internal/gitlock"
	"github.com/magefile/mage/sh"
)

const maxRetries = 3

// gitOutput runs `git <args...>` under the repository-wide gitlock and
// returns its output exactly as sh.Output would, only serialized against the
// other languages generating concurrently.
func gitOutput(args ...string) (string, error) {
	var out string
	var outErr error
	if err := gitlock.Do(func() error {
		out, outErr = sh.Output("git", args...)
		return nil
	}); err != nil {
		return "", err
	}
	return out, outErr
}

// gitRun runs `git <args...>` under the repository-wide gitlock, exactly as
// sh.Run would, only serialized against the other languages generating
// concurrently. Unlike the sh.Run calls this replaces, its error must not be
// discarded: a failed rollback leaves this language's half-generated output
// in the tree for the root Magefile's commitIfChanged to sweep up.
func gitRun(args ...string) error {
	return gitlock.Do(func() error {
		return sh.Run("git", args...)
	})
}

// Gen regenerates the proto client: runs buf generate, then invokes Claude
// to add boilerplate per DESIGN.md. If claude is not available (e.g. in
// gen-nodiff CI), any buf generate changes are rolled back and Gen returns
// nil so the working tree stays clean for the nodiff check.
func Gen() error {
	fmt.Println("==> Running buf generate...")
	// BUF_TEMPLATE is set by the root Magefile's genProtoLangs when BUFTAG pins
	// the upstream API to an exact BSR revision. Unset means generate from the
	// checked-in buf.gen.yaml, which is the normal local-development path.
	genArgs := []string{"generate"}
	if tmpl := os.Getenv("BUF_TEMPLATE"); tmpl != "" {
		genArgs = append(genArgs, "--template", tmpl)
	}
	if err := sh.Run("buf", genArgs...); err != nil {
		return fmt.Errorf("buf generate failed: %w", err)
	}

	diff, _ := gitOutput("diff", "--name-only", ".")
	if strings.TrimSpace(diff) == "" {
		fmt.Println("==> No proto changes detected after buf generate, skipping Claude step.")
		return nil
	}

	if !clauderun.Available() {
		fmt.Println("==> claude not available; rolling back buf generate changes (gen-nodiff mode).")
		if err := gitRun("checkout", "--", "."); err != nil {
			return fmt.Errorf("claude unavailable and rollback (git checkout) failed, tree may be dirty: %w", err)
		}
		if err := gitRun("clean", "-fd", "."); err != nil {
			return fmt.Errorf("claude unavailable and rollback (git clean) failed, tree may be dirty: %w", err)
		}
		return nil
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := clauderun.Run(
		"Read DESIGN.md. Review the generated code under gen/. " +
			"Add the additional code specified in the manifest (client.go, types.go, client_test.go). " +
			"Run `go test ./...` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("go", "test", "-v", "./..."); err == nil {
			fmt.Println("==> Tests passed!")
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
			if err := gitRun("checkout", "--", "."); err != nil {
				return fmt.Errorf("tests failed after %d retries, and rollback (git checkout) also failed: %w", maxRetries, err)
			}
			if err := gitRun("clean", "-fd", "."); err != nil {
				return fmt.Errorf("tests failed after %d retries, and rollback (git clean) also failed: %w", maxRetries, err)
			}
			return fmt.Errorf("tests failed after %d retries", maxRetries)
		}

		fmt.Println("==> Tests failed, asking Claude to fix...")
		if err := clauderun.Run("Tests failed. Read the test output above and fix the issues."); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}
	}

	return nil
}

// Test runs the proto client tests.
func Test() error {
	return sh.RunV("go", "test", "-v", "./...")
}
