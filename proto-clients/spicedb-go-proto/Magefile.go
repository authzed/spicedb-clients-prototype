//go:build mage

package main

import (
	"fmt"

	"github.com/magefile/mage/sh"
)

const maxRetries = 3

// Gen regenerates the proto client: runs buf generate, then invokes Claude
// to add boilerplate per DESIGN.md.
func Gen() error {
	fmt.Println("==> Running buf generate...")
	if err := sh.Run("buf", "generate"); err != nil {
		return fmt.Errorf("buf generate failed: %w", err)
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := sh.Run("claude", "-p",
		"Read DESIGN.md. Review the generated code under gen/. "+
			"Add the additional code specified in the manifest (client.go, types.go, client_test.go). "+
			"Run `go test ./...` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		out, err := sh.Output("go", "test", "./...")
		if err == nil {
			fmt.Println("==> Tests passed!")
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
			_ = sh.Run("git", "checkout", "--", ".")
			return fmt.Errorf("tests failed after %d retries:\n%s", maxRetries, out)
		}

		fmt.Println("==> Tests failed, asking Claude to fix...")
		if err := sh.Run("claude", "-p",
			fmt.Sprintf("Tests failed with the following output. Fix the issues:\n\n%s", out),
		); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}
	}

	return nil
}

// Test runs the proto client tests.
func Test() error {
	return sh.Run("go", "test", "./...")
}
