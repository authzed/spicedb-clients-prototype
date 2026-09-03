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

// Gen exports protos via buf, invokes Claude to wire up the client, then tests.
// If claude is not available (e.g. in gen-nodiff CI), any buf export changes
// are rolled back and Gen returns nil so the working tree stays clean for the
// nodiff check.
func Gen() error {
	fmt.Println("==> Exporting protos via buf...")
	// This client has no buf.gen.yaml -- it exports raw protos and generates via
	// build.rs -- so there is only one input and the positional form is correct
	// here. BUFTAG is set by the root Magefile's genProtoLangs.
	exportInput := "buf.build/authzed/api"
	if ref := os.Getenv("BUFTAG"); ref != "" {
		exportInput += ":" + ref
	}
	if err := sh.Run("buf", "export", exportInput, "-o", "proto"); err != nil {
		return fmt.Errorf("buf export failed: %w", err)
	}

	diff, _ := sh.Output("git", "diff", "--name-only", "proto")
	if strings.TrimSpace(diff) == "" {
		fmt.Println("==> No proto changes detected after buf export, skipping Claude step.")
		return nil
	}

	if !clauderun.Available() {
		fmt.Println("==> claude not available; rolling back buf export changes (gen-nodiff mode).")
		_ = sh.Run("git", "checkout", "--", "proto")
		return nil
	}

	fmt.Println("==> Invoking Claude to wire up generated code...")
	if err := clauderun.Run(
		"Read DESIGN.md. The proto/ directory has been populated by buf export. " +
			"Uncomment the tonic::include_proto! declarations in src/lib.rs, " +
			"wire up the generated service clients in src/client.rs, " +
			"and update the tests in tests/client_test.rs. " +
			"Run `cargo test` to verify. Fix any failures.",
	); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("cargo", "test"); err == nil {
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

// Test runs the Rust proto client tests.
func Test() error {
	return sh.RunV("cargo", "test")
}
