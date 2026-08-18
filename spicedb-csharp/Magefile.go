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
	protoClientDir = "../proto-clients/spicedb-csharp-proto"
	lastGenFile    = ".last-generation"

	// Examples are xunit projects that require a live SpiceDB, so they cannot
	// join SpiceDB.Client.sln -- the unit job runs that one and has no server.
	// They get their own solution instead, built by Test and run by
	// IntegrationTest. Without it nothing in examples/ was compiled or run by
	// CI at all.
	examplesSolution = "SpiceDB.Client.Examples.sln"
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
			"Ensure all tests still pass. "+
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

		// Build + run tests
		if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
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

		if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--no-build", "--verbosity", "normal"); err == nil {
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

// Test builds and runs all tests, and compiles the examples.
//
// The examples are built but NOT run here: each one is an xunit project that
// talks to a real SpiceDB, so running them belongs in IntegrationTest. They
// are still compiled, because a signature change that breaks an example
// otherwise stays invisible until the integration job -- and, before the
// examples solution existed, stayed invisible entirely: no example project
// was in any solution, so `dotnet test SpiceDB.Client.sln` never touched
// them.
func Test() error {
	if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
		return err
	}
	if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--no-build", "--verbosity", "normal"); err != nil {
		return err
	}
	return sh.RunV("dotnet", "build", examplesSolution)
}

// Lint runs dotnet format to check code style.
func Lint() error {
	return sh.RunV("dotnet", "format", "SpiceDB.Client.sln", "--verify-no-changes")
}

// ApiCompat checks for breaking API changes against the given base git ref.
func ApiCompat(baseRef string) error {
	if _, err := exec.LookPath("apicompat"); err != nil {
		return fmt.Errorf("apicompat not found. Install with: dotnet tool install --global Microsoft.DotNet.ApiCompat.Tool")
	}

	fmt.Printf("==> Checking C# API compatibility against %s...\n", baseRef)

	// Build current version
	if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
		return fmt.Errorf("current build failed: %w", err)
	}

	currentDLL, err := filepath.Abs("SpiceDB.Client/bin/Debug/net10.0/SpiceDB.Client.dll")
	if err != nil {
		return err
	}

	// Create a temporary worktree at the baseline ref.
	// MkdirTemp creates the directory, but git worktree add needs it to not exist,
	// so we remove it first and let git recreate it.
	worktreeDir, err := os.MkdirTemp("", "spicedb-csharp-baseline-*")
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

	// Build only the library project in the worktree (not the .sln which includes tests).
	// Restore packages first since the worktree has no obj/ or NuGet cache.
	baselineDir := filepath.Join(worktreeDir, "spicedb-csharp")
	baselineCsproj := filepath.Join(baselineDir, "SpiceDB.Client", "SpiceDB.Client.csproj")
	if err := sh.RunV("dotnet", "restore", filepath.Join(baselineDir, "SpiceDB.Client.sln")); err != nil {
		return fmt.Errorf("baseline restore failed: %w", err)
	}
	if err := sh.RunV("dotnet", "build", baselineCsproj, "--no-restore"); err != nil {
		return fmt.Errorf("baseline build failed: %w", err)
	}

	baselineDLL := filepath.Join(baselineDir, "SpiceDB.Client", "bin", "Debug", "net10.0", "SpiceDB.Client.dll")

	// Compare: left = baseline (old contract), right = current (new implementation)
	if err := sh.RunV("apicompat", "--left", baselineDLL, "--right", currentDLL); err != nil {
		return fmt.Errorf("API compatibility check failed: breaking changes detected. Run 'mage updateAllowBreak' to proceed: %w", err)
	}
	fmt.Println("==> spicedb-csharp: API compatible")
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

	// Run integration tests (no --no-build: VSTest cannot load .NET 10 assemblies
	// without a fresh build step, and the build is fast enough to not matter here)
	fmt.Println("==> Running integration tests...")
	if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--verbosity", "normal"); err != nil {
		return err
	}

	// Then the examples, which are the integration suite proper -- each one
	// drives a real SpiceDB. They live in their own solution because they
	// cannot run in the unit job, which has no server.
	fmt.Println("==> Running examples against SpiceDB...")
	return sh.RunV("dotnet", "test", examplesSolution, "--verbosity", "normal")
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
