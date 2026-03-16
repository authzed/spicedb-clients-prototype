//go:build mage

package main

import (
	"fmt"

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
	if err := sh.Run("yarn", "install"); err != nil {
		return fmt.Errorf("yarn install failed: %w", err)
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := sh.Run("claude", "-p",
		"Read DESIGN.md. Review the generated code under src/gen/. "+
			"Add the additional code specified in the manifest (src/client.ts, src/index.ts, src/__tests__/client.test.ts). "+
			"Run `yarn build && yarn test` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running build + tests (attempt %d/%d)...\n", attempt, maxRetries)

		if err := sh.Run("yarn", "build"); err != nil {
			if attempt == maxRetries {
				_ = sh.Run("git", "checkout", "--", ".")
				return fmt.Errorf("build failed after %d retries", maxRetries)
			}
			fmt.Println("==> Build failed, asking Claude to fix...")
			out, _ := sh.Output("yarn", "build")
			_ = sh.Run("claude", "-p", fmt.Sprintf("Build failed:\n\n%s\n\nFix the issues.", out))
			continue
		}

		out, err := sh.Output("yarn", "test")
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
		_ = sh.Run("claude", "-p", fmt.Sprintf("Tests failed:\n\n%s\n\nFix the issues.", out))
	}

	return nil
}

// Test runs the TypeScript proto client tests.
func Test() error {
	if err := sh.Run("yarn", "build"); err != nil {
		return err
	}
	return sh.Run("yarn", "test")
}
