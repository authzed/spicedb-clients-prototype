//go:build mage

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
)

const (
	maxRetries     = 3
	protoClientDir = "../proto-clients/spicedb-go-proto"
	lastGenFile    = ".last-generation"
)

// Gen updates the idiomatic client based on proto client changes.
func Gen() error {
	// Read last generation baseline
	baseline, err := os.ReadFile(lastGenFile)
	if err != nil {
		baseline = []byte("HEAD~1")
	}

	// Compute proto diff
	diff, err := sh.Output("git", "diff", strings.TrimSpace(string(baseline)), "--", protoClientDir)
	if err != nil {
		// If baseline commit doesn't exist, diff against HEAD~1
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
	if err := runClaude(prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)

		// Build examples + run tests
		if err := sh.RunV("go", "build", "./..."); err != nil {
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

		if err := sh.RunV("go", "test", "-v", "./..."); err == nil {
			fmt.Println("==> Tests passed!")
			// Update baseline
			head, _ := sh.Output("git", "rev-parse", "HEAD")
			_ = os.WriteFile(lastGenFile, []byte(strings.TrimSpace(head)), 0644)
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

// Test runs all tests and builds all examples.
func Test() error {
	if err := sh.RunV("go", "build", "./..."); err != nil {
		return err
	}
	return sh.RunV("go", "test", "-v", "./...")
}

// Lint runs golangci-lint on all Go code.
func Lint() error {
	return sh.RunV("golangci-lint", "run", "./...")
}

// IntegrationTest starts SpiceDB via Docker and runs examples against it.
func IntegrationTest() error {
	// Start SpiceDB
	fmt.Println("==> Starting SpiceDB...")
	if err := sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	defer func() {
		fmt.Println("==> Stopping SpiceDB...")
		_ = sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "down")
	}()

	// Wait for SpiceDB to be ready
	fmt.Println("==> Waiting for SpiceDB to be ready...")
	if err := waitForReady("localhost:50051", 30*time.Second); err != nil {
		return err
	}

	// Run each example (except watch_changes)
	examples, err := filepath.Glob("examples/*/main.go")
	if err != nil {
		return fmt.Errorf("glob examples failed: %w", err)
	}

	var failures []string
	for _, ex := range examples {
		dir := filepath.Dir(ex)
		name := filepath.Base(dir)
		if name == "watch_changes" {
			fmt.Printf("==> Skipping %s (infinite stream)\n", name)
			continue
		}
		fmt.Printf("==> Running example: %s\n", name)
		if err := sh.RunV("go", "run", "./"+dir); err != nil {
			failures = append(failures, name)
			fmt.Printf("==> FAIL: %s\n", name)
		} else {
			fmt.Printf("==> PASS: %s\n", name)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("integration tests failed: %s", strings.Join(failures, ", "))
	}
	fmt.Println("==> All integration tests passed!")
	return nil
}

func waitForReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			// Extra delay for gRPC server to finish initialization after port is open
			time.Sleep(3 * time.Second)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("SpiceDB not ready at %s after %s", addr, timeout)
}

// runClaude pipes the prompt to claude via stdin so output streams in real time.
func runClaude(prompt string) error {
	cmd := exec.Command("claude")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
