//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/magefile/mage/sh"
)

const maxRetries = 3

// Gen regenerates the Python proto client.
func Gen() error {
	fmt.Println("==> Running buf generate...")
	if err := sh.Run("buf", "generate"); err != nil {
		return fmt.Errorf("buf generate failed: %w", err)
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

func runClaude(prompt string) error {
	cmd := exec.Command("claude", "-p", prompt, "--verbose", "--output-format", "text")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
