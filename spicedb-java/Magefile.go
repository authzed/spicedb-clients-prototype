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
	protoClientDir = "../proto-clients/spicedb-java-proto"
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

		if err := sh.RunV("./gradlew", "build"); err != nil {
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

		if err := sh.RunV("./gradlew", "test"); err == nil {
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

// Test runs all tests via Gradle.
func Test() error {
	return sh.RunV("./gradlew", "test")
}

// Build compiles all source via Gradle.
func Build() error {
	return sh.RunV("./gradlew", "build")
}

// Lint runs Spotless check for code formatting.
func Lint() error {
	return sh.RunV("./gradlew", "spotlessCheck")
}

// ApiCompat checks for breaking API changes against the given base git ref.
func ApiCompat(baseRef string) error {
	// Find japicmp JAR in tools/ dir
	japicmpJar := filepath.Join("..", "tools", "japicmp.jar")
	if _, err := os.Stat(japicmpJar); err != nil {
		return fmt.Errorf("japicmp not found. Download japicmp-*-jar-with-dependencies.jar from " +
			"https://github.com/siom79/japicmp/releases and place at tools/japicmp.jar")
	}

	fmt.Printf("==> Checking Java API compatibility against %s...\n", baseRef)

	// Build current version
	if err := sh.RunV("./gradlew", "build"); err != nil {
		return fmt.Errorf("current build failed: %w", err)
	}

	currentJAR, err := filepath.Abs("lib/build/libs/lib.jar")
	if err != nil {
		return err
	}

	// Create a temporary worktree at the baseline ref.
	// MkdirTemp creates the directory, but git worktree add needs it to not exist,
	// so we remove it first and let git recreate it.
	worktreeDir, err := os.MkdirTemp("", "spicedb-java-baseline-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	_ = os.Remove(worktreeDir)
	defer func() {
		_ = sh.Run("git", "-C", "..", "worktree", "remove", "--force", worktreeDir)
		_ = os.RemoveAll(worktreeDir)
	}()

	if err := sh.RunV("git", "-C", "..", "worktree", "add", worktreeDir, baseRef); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}

	// Build baseline in the worktree
	baselineDir := filepath.Join(worktreeDir, "spicedb-java")
	if err := sh.RunV(filepath.Join(baselineDir, "gradlew"), "-p", baselineDir, "build"); err != nil {
		return fmt.Errorf("baseline build failed: %w", err)
	}

	baselineJAR := filepath.Join(baselineDir, "lib", "build", "libs", "lib.jar")

	japicmpJar, _ = filepath.Abs(japicmpJar)

	// Compare: -o = old (baseline), -n = new (current)
	if err := sh.RunV("java", "-jar", japicmpJar, "-o", baselineJAR, "-n", currentJAR,
		"--only-incompatible", "--ignore-missing-classes"); err != nil {
		return fmt.Errorf("API compatibility check failed: breaking changes detected. Run 'mage updateAllowBreak' to proceed: %w", err)
	}
	fmt.Println("==> spicedb-java: API compatible")
	return nil
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

	// Run integration tests via Gradle
	fmt.Println("==> Running integration tests...")
	return sh.RunV("./gradlew", "test")
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
