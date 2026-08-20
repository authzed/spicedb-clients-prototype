//go:build mage

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
)

const (
	maxRetries     = 3
	protoClientDir = "../proto-clients/spicedb-typescript-proto"
	lastGenFile    = ".last-generation"

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which every example that uses the
	// shared server reads, so the suite can be pointed at a SpiceDB on another
	// port when 50051 is taken.
	defaultEndpoint = "localhost:50051"
	defaultToken    = "testtoken"
)

// wantExamples is every example this runner expects under examples/, by name.
// The set is pinned rather than counted: a count alone still passes when an
// example is renamed, and a glob cannot list an example that is not there, so a
// moved or renamed example would otherwise just shrink the run and still report
// green. Add a name here when adding an example. See root DESIGN.md, "RULE: An
// example must be executed by CI and must be able to fail", clause 1.
var wantExamples = []string{
	"bulk_operations",
	"call_deadlines",
	"check_permission",
	"custom_tls",
	"error_mapping",
	"expand_permission_tree",
	"insecure_opt_in",
	"lookup_resources",
	"lookup_subjects",
	"raw_escape_hatch",
	"read_relationships",
	"retry_policy",
	"schema_management",
	"unrepresentable_values",
	"watch_changes",
	"write_relationships",
}

// skippedExamples maps an example that IntegrationTest does not execute to the
// reason it does not. Every other example under examples/ MUST run. A skip has
// to be listed here to happen at all, so it is visible in the run's output and
// counted against wantExamples -- never the silent residue of a filter.
//
// It is empty on purpose. watch_changes used to live here -- "open-ended
// stream; needs a bounded consumer with explicit cancellation" -- which meant
// the only streaming example never ran, and root DESIGN.md's "RULE: Abandoning
// a stream must release it" had no executed coverage at all. It now has that
// bounded consumer, so nothing is skipped.
var skippedExamples = map[string]string{}

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
	if err := runClaude(prompt); err != nil {
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

// Test builds, checks the example wiring, and runs TypeScript tests.
func Test() error {
	if err := sh.RunV("pnpm", "build"); err != nil {
		return err
	}
	if err := CheckExamples(); err != nil {
		return err
	}
	return sh.RunV("pnpm", "test")
}

// CheckExamples verifies the example wiring without needing a server: that the
// glob still matches the expected number of examples, and that every name the
// integration runner skips is an example that exists. It is cheap, so Test runs
// it too.
func CheckExamples() error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	fmt.Printf("==> spicedb-typescript: %d examples on disk, %d skipped by the integration runner\n",
		len(names), len(skippedExamples))
	return nil
}

// exampleTargets returns the sorted example names on disk, after asserting the
// set is the one this runner expects.
//
// The count assertion is what makes a rename fail loudly: a glob cannot list an
// example that is not there, so without it a moved or renamed file just shrinks
// the run and still reports green.
func exampleTargets() ([]string, error) {
	files, err := filepath.Glob("examples/*/index.ts")
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(files)

	names := make([]string, 0, len(files))
	onDisk := make(map[string]bool, len(files))
	for _, f := range files {
		name := filepath.Base(filepath.Dir(f))
		names = append(names, name)
		onDisk[name] = true
	}
	if err := reconcile("examples/*/index.ts", names, onDisk); err != nil {
		return nil, err
	}
	return names, nil
}

// Lint type-checks all TypeScript code including examples.
func Lint() error {
	if err := sh.RunV("pnpm", "build"); err != nil {
		return err
	}
	return sh.RunV("npx", "tsc", "--noEmit", "-p", "tsconfig.examples.json")
}

// ApiCompat checks for breaking API changes against the committed API report.
// The baseRef parameter is accepted for interface consistency but ignored —
// TypeScript uses a snapshot-based approach via @microsoft/api-extractor.
func ApiCompat(baseRef string) error {
	fmt.Println("==> Checking TypeScript API compatibility against committed report...")

	// Build first to generate .d.ts files
	if err := sh.RunV("pnpm", "build"); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	if err := sh.RunV("pnpm", "exec", "api-extractor", "run"); err != nil {
		return fmt.Errorf("API compatibility check failed: API report has changed. "+
			"If this is intentional, run 'pnpm exec api-extractor run --local' to update the report, "+
			"then commit the updated etc/*.api.md. Run 'mage updateAllowBreak' to skip this check: %w", err)
	}
	fmt.Println("==> spicedb-typescript: API compatible")
	return nil
}

// IntegrationTest starts SpiceDB via Docker and runs examples against it.
func IntegrationTest() error {
	endpoint := envOr("SPICEDB_ENDPOINT", defaultEndpoint)
	token := envOr("SPICEDB_TOKEN", defaultToken)

	// Publish the container on whatever port the endpoint names, so a caller
	// whose 50051 is occupied can run the suite by setting SPICEDB_ENDPOINT
	// alone.
	port, err := portOf(endpoint)
	if err != nil {
		return err
	}
	composeEnv := map[string]string{"SPICEDB_TEST_PORT": port, "SPICEDB_TEST_TOKEN": token}

	fmt.Println("==> Starting SpiceDB...")
	if err := sh.RunWithV(composeEnv, "docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	defer func() {
		fmt.Println("==> Stopping SpiceDB...")
		_ = sh.RunWithV(composeEnv, "docker", "compose", "-f", "docker-compose.test.yml", "down")
	}()

	fmt.Println("==> Waiting for SpiceDB to be ready...")
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	// Fail before the slow part if the example set on disk is not the one this
	// runner expects to execute.
	names, err := exampleTargets()
	if err != nil {
		return err
	}

	clientEnv := map[string]string{"SPICEDB_ENDPOINT": endpoint, "SPICEDB_TOKEN": token}
	var executed, failures []string
	for _, name := range names {
		if reason, skipped := skippedExamples[name]; skipped {
			fmt.Printf("==> SKIP %s (%s)\n", name, reason)
			continue
		}
		fmt.Printf("==> Running example: %s\n", name)
		if err := sh.RunWithV(clientEnv, "npx", "tsx", filepath.Join("examples", name, "index.ts")); err != nil {
			failures = append(failures, name)
			fmt.Printf("==> FAIL: %s\n", name)
		} else {
			fmt.Printf("==> PASS: %s\n", name)
		}
		executed = append(executed, name)
	}

	// Belt-and-braces, not the guard. reconcile has already proved that disk
	// equals wantExamples and that every skip key names an example that
	// exists, so by construction this loop ran exactly this many -- the check
	// can only fire if the loop above is edited to skip something silently.
	// What actually protects the run is reconcile (which set ran) plus the
	// failures list below (whether they passed).
	wantExecuted := len(wantExamples) - len(skippedExamples)
	if len(executed) != wantExecuted {
		return fmt.Errorf("executed %d examples, want %d (%d on disk, %d skipped)",
			len(executed), wantExecuted, len(names), len(skippedExamples))
	}

	if len(failures) > 0 {
		return fmt.Errorf("integration tests failed: %s", strings.Join(failures, ", "))
	}
	fmt.Printf("==> All %d examples passed (%d skipped).\n", len(executed), len(skippedExamples))
	return nil
}

// reconcile compares the example names found on disk against wantExamples in
// both directions, and checks that every skip target still exists.
//
// The set is pinned by name rather than counted: a count alone still passes
// when an example is *renamed*, and a glob cannot list an example that is not
// there, so a moved or renamed example would otherwise just shrink the run and
// still report green.
func reconcile(glob string, names []string, onDisk map[string]bool) error {
	want := make(map[string]bool, len(wantExamples))
	for _, name := range wantExamples {
		want[name] = true
	}

	var missing, unexpected []string
	for _, name := range wantExamples {
		if !onDisk[name] {
			missing = append(missing, name)
		}
	}
	for _, name := range names {
		if !want[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)

	if len(missing) > 0 || len(unexpected) > 0 {
		return fmt.Errorf(
			"%s does not match wantExamples in Magefile.go -- expected but absent: [%s]; "+
				"present but not expected: [%s]. Update wantExamples if the change is intended",
			glob, strings.Join(missing, ", "), strings.Join(unexpected, ", "))
	}
	for name := range skippedExamples {
		if !onDisk[name] {
			return fmt.Errorf("skippedExamples names %q, which is not an example on disk: "+
				"a renamed skip target would otherwise silently start being skipped by nothing", name)
		}
	}
	return nil
}

// envOr returns the value of the named environment variable, or fallback when
// it is unset or empty.
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// portOf returns the port component of a host:port endpoint.
func portOf(endpoint string) (string, error) {
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("SPICEDB_ENDPOINT %q is not host:port: %w", endpoint, err)
	}
	return port, nil
}

func waitForReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
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
