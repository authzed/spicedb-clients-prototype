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

// claudeAvailable returns true if the claude CLI is installed and authenticated.
func claudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// Gen regenerates the Python proto client. If claude is not available (e.g. in
// gen-nodiff CI), any buf generate changes are rolled back and Gen returns nil
// so the working tree stays clean for the nodiff check.
func Gen() error {
	fmt.Println("==> Running buf generate...")
	if err := sh.Run("buf", "generate"); err != nil {
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
		return nil
	}

	fmt.Println("==> Syncing Python deps...")
	if err := sh.RunV("uv", "sync"); err != nil {
		return fmt.Errorf("uv sync failed: %w", err)
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := runClaude(
		"Read DESIGN.md. Review the generated code under gen/. " +
			"Add the additional code specified in the manifest (client.py, __init__.py, tests/test_client.py). " +
			"Run `uv run pytest` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("uv", "run", "pytest", "-v"); err == nil {
			fmt.Println("==> Tests passed!")
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
			_ = sh.Run("git", "checkout", "--", ".")
			return fmt.Errorf("tests failed after %d retries", maxRetries)
		}

		fmt.Println("==> Tests failed, asking Claude to fix...")
		if err := runClaude("Tests failed. Read the test output above and fix the issues."); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}
	}

	return nil
}

// Test runs the Python proto client tests.
func Test() error {
	return sh.RunV("uv", "run", "pytest", "-v")
}

// runClaude pipes the prompt to claude via stdin so output streams in real time.
func runClaude(prompt string) error {
	cmd := exec.Command("claude")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
