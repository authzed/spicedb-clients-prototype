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

// Gen regenerates the TypeScript proto client.
func Gen() error {
	fmt.Println("==> Running buf generate...")
	if err := sh.Run("buf", "generate"); err != nil {
		return fmt.Errorf("buf generate failed: %w", err)
	}

	fmt.Println("==> Installing deps...")
	if err := sh.RunV("pnpm", "install"); err != nil {
		return fmt.Errorf("pnpm install failed: %w", err)
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := runClaude(
		"Read DESIGN.md. Review the generated code under src/gen/. " +
			"Add the additional code specified in the manifest (src/client.ts, src/index.ts, src/__tests__/client.test.ts). " +
			"Run `pnpm build && pnpm test` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running build + tests (attempt %d/%d)...\n", attempt, maxRetries)

		fmt.Println("==> Building...")
		if err := sh.RunV("pnpm", "build"); err != nil {
			if attempt == maxRetries {
				_ = sh.Run("git", "checkout", "--", ".")
				return fmt.Errorf("build failed after %d retries", maxRetries)
			}
			fmt.Println("==> Build failed, asking Claude to fix...")
			if err := runClaude("Build failed. Read the build output above and fix the issues."); err != nil {
				return fmt.Errorf("claude fix invocation failed: %w", err)
			}
			continue
		}

		fmt.Println("==> Running tests...")
		if err := sh.RunV("pnpm", "test"); err == nil {
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

// Test runs the TypeScript proto client tests.
func Test() error {
	if err := sh.RunV("pnpm", "build"); err != nil {
		return err
	}
	return sh.RunV("pnpm", "test")
}

// runClaude pipes the prompt to claude via stdin so output streams in real time.
func runClaude(prompt string) error {
	cmd := exec.Command("claude")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
