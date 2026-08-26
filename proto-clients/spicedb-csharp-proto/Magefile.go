//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/magefile/mage/sh"
)

const maxRetries = 3

// claudeAvailable returns true if the claude CLI is installed and usable.
// Returns false when running in CI (CI env var set) because the claude binary
// may be present but not authenticated.
//
// CI_REGENERATION is the one exception, and it is set by exactly one workflow:
// .github/workflows/regen-from-api.yaml, which is also the only workflow holding
// Claude credentials. Every other CI job -- notably meta.yaml's gen-nodiff --
// must keep taking the false branch here, or `mage gen:all` starts making
// unreviewed changes inside a check whose whole purpose is asserting that
// generation produces no diff.
func claudeAvailable() bool {
	if os.Getenv("CI") != "" && os.Getenv("CI_REGENERATION") == "" {
		return false
	}
	_, err := exec.LookPath("claude")
	return err == nil
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

	diff, _ := sh.Output("git", "diff", "--name-only", ".")
	if strings.TrimSpace(diff) == "" {
		fmt.Println("==> No proto changes detected after buf generate, skipping Claude step.")
		return nil
	}

	if !claudeAvailable() {
		fmt.Println("==> claude not available; rolling back buf generate changes (gen-nodiff mode).")
		_ = sh.Run("git", "checkout", "--", ".")
		_ = sh.Run("git", "clean", "-fd", ".")
		return nil
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := runClaude(
		"Read DESIGN.md. Review the generated code under gen/. " +
			"Add the additional code specified in the manifest (Client.cs, ClientTest.cs). " +
			"Run `dotnet test` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("dotnet", "test", "--verbosity", "normal"); err == nil {
			fmt.Println("==> Tests passed!")
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
			_ = sh.Run("git", "checkout", "--", ".")
			_ = sh.Run("git", "clean", "-fd", ".")
			return fmt.Errorf("tests failed after %d retries", maxRetries)
		}

		fmt.Println("==> Tests failed, asking Claude to fix...")
		if err := runClaude("Tests failed. Read the test output above and fix the issues."); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}
	}

	return nil
}

// Test runs the proto client tests.
func Test() error {
	return sh.RunV("dotnet", "test", "--verbosity", "normal")
}

// runClaude pipes the prompt to claude via stdin so output streams in real time.
func runClaude(prompt string) error {
	cmd := exec.Command("claude")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
