//go:build mage

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/authzed/spicedb-clients/internal/clauderun"
	"github.com/magefile/mage/sh"
)

const maxRetries = 3

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

	if !clauderun.Available() {
		fmt.Println("==> claude not available; rolling back buf generate changes (gen-nodiff mode).")
		_ = sh.Run("git", "checkout", "--", ".")
		return nil
	}

	fmt.Println("==> Installing Ruby dependencies...")
	if err := sh.Run("bundle", "install"); err != nil {
		return fmt.Errorf("bundle install failed: %w", err)
	}

	fmt.Println("==> Invoking Claude to add boilerplate...")
	if err := clauderun.Run(
		"Read DESIGN.md. Review the generated code under lib/gen/. " +
			"Add the additional code specified in the manifest (lib/spicedb_proto/client.rb, spec/client_spec.rb). " +
			"Run `bundle exec rspec` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("bundle", "exec", "rspec"); err == nil {
			fmt.Println("==> Tests passed!")
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
			_ = sh.Run("git", "checkout", "--", ".")
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
	return sh.RunV("bundle", "exec", "rspec")
}
