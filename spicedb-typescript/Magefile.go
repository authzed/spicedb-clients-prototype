//go:build mage

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/magefile/mage/sh"
)

const (
	maxRetries     = 3
	protoClientDir = "../proto-clients/spicedb-typescript-proto"
	lastGenFile    = ".last-generation"
)

// Gen updates the idiomatic TypeScript client based on proto client changes.
func Gen() error {
	baseline, err := os.ReadFile(lastGenFile)
	if err != nil {
		baseline = []byte("HEAD~1")
	}

	diff, err := sh.Output("git", "diff", strings.TrimSpace(string(baseline)), "--", protoClientDir)
	if err != nil {
		diff, _ = sh.Output("git", "diff", "HEAD~1", "--", protoClientDir)
	}

	if diff == "" {
		fmt.Println("==> No proto changes detected, skipping.")
		return nil
	}

	prompt := fmt.Sprintf(
		"The proto client has changed. Here is the diff:\n\n%s\n\n"+
			"Read ../DESIGN.md and ./DESIGN.md. Update this client accordingly. "+
			"Ensure all examples still compile and pass. "+
			"Add new examples for new functionality. "+
			"Update DESIGN.md changelog if needed.",
		diff,
	)

	fmt.Println("==> Invoking Claude to update idiomatic client...")
	if err := sh.Run("claude", "-p", prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running build + tests (attempt %d/%d)...\n", attempt, maxRetries)

		if err := sh.Run("pnpm", "build"); err != nil {
			if attempt == maxRetries {
				_ = sh.Run("git", "checkout", "--", ".")
				return fmt.Errorf("build failed after %d retries", maxRetries)
			}
			out, _ := sh.Output("pnpm", "build")
			_ = sh.Run("claude", "-p", fmt.Sprintf("Build failed:\n\n%s\n\nFix the issues.", out))
			continue
		}

		out, err := sh.Output("pnpm", "test")
		if err == nil {
			fmt.Println("==> Tests passed!")
			head, _ := sh.Output("git", "rev-parse", "HEAD")
			_ = os.WriteFile(lastGenFile, []byte(strings.TrimSpace(head)), 0644)
			return nil
		}

		if attempt == maxRetries {
			_ = sh.Run("git", "checkout", "--", ".")
			return fmt.Errorf("tests failed after %d retries:\n%s", maxRetries, out)
		}

		_ = sh.Run("claude", "-p", fmt.Sprintf("Tests failed:\n\n%s\n\nFix the issues.", out))
	}

	return nil
}

// Test builds and runs TypeScript tests.
func Test() error {
	if err := sh.Run("pnpm", "build"); err != nil {
		return err
	}
	return sh.Run("pnpm", "test")
}
