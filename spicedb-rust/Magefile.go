//go:build mage

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
)

const (
	maxRetries     = 3
	protoClientDir = "../proto-clients/spicedb-rust-proto"
	lastGenFile    = ".last-generation"

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which every example reads, so the
	// suite can be pointed at a SpiceDB on another port when 50051 is taken.
	defaultEndpoint = "localhost:50051"
	defaultToken    = "testtoken"

	// Number of #[ignore]d functions in tests/live_integration_test.rs. A name
	// filter that selects nothing makes libtest exit 0, so the count the run
	// reports is asserted rather than trusted -- the same reason
	// .github/workflows/rust.yaml greps its output for "1 passed".
	wantLiveTestCount = 2
)

// wantExamples is every example this runner expects under examples/, by name.
// The set is pinned rather than counted: a count alone still passes when an
// example is renamed, and a glob cannot list an example that is not there, so a
// moved or renamed file would otherwise just shrink the run and still report
// green. Add a name here when adding an example. See root DESIGN.md, "RULE: An
// example must be executed by CI and must be able to fail", clause 1.
var wantExamples = []string{
	"bulk_operations",
	"call_deadlines",
	"check_permission",
	"expand_permission_tree",
	"lookup_resources",
	"lookup_subjects",
	"raw_escape_hatch",
	"read_relationships",
	"relationship_counters",
	"schema_management",
	"schema_reflection",
	"watch_changes",
	"write_relationships",
}

// skippedExamples maps an example that IntegrationTest does not execute to the
// reason it does not. Every other example under examples/ MUST run. A skip has
// to be listed here to happen at all, so it is visible in the run's output and
// counted against wantExamples -- never the silent residue of a filter.
// libtestResult matches libtest's per-binary summary line, e.g.
// "test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out".
var libtestResult = regexp.MustCompile(`test result: \w+\. (\d+) passed;`)

var skippedExamples = map[string]string{
	"watch_changes": "open-ended stream; needs a bounded consumer with explicit cancellation",
}

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

		// Build + clippy + test
		if err := sh.RunV("cargo", "clippy", "--all-targets", "--", "-D", "warnings"); err != nil {
			if attempt == maxRetries {
				_ = sh.Run("git", "checkout", "--", ".")
				return fmt.Errorf("clippy failed after %d retries", maxRetries)
			}
			fmt.Println("==> Clippy failed, asking Claude to fix...")
			if err := runClaude("Clippy failed. Read the output above and fix the issues."); err != nil {
				return fmt.Errorf("claude fix invocation failed: %w", err)
			}
			continue
		}

		if err := sh.RunV("cargo", "test"); err == nil {
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

// Test runs cargo test and cargo clippy, and checks the example wiring.
func Test() error {
	if err := sh.RunV("cargo", "fmt", "--check"); err != nil {
		return err
	}
	// --all-targets so examples/ and tests/ are linted too: without it a
	// clippy failure in example code was invisible.
	if err := sh.RunV("cargo", "clippy", "--all-targets", "--", "-D", "warnings"); err != nil {
		return err
	}
	if err := CheckExamples(); err != nil {
		return err
	}
	return sh.RunV("cargo", "test")
}

// Lint runs cargo clippy with warnings as errors.
func Lint() error {
	return sh.RunV("cargo", "clippy", "--all-targets", "--", "-D", "warnings")
}

// Fmt checks that all Rust code is formatted.
func Fmt() error {
	return sh.RunV("cargo", "fmt", "--check")
}

// ApiCompat checks for breaking API changes against the given base git ref.
func ApiCompat(baseRef string) error {
	if _, err := exec.LookPath("cargo-semver-checks"); err != nil {
		return fmt.Errorf("cargo-semver-checks not found. Install with: cargo install cargo-semver-checks (or: brew install cargo-semver-checks)")
	}

	fmt.Printf("==> Checking Rust API compatibility against %s...\n", baseRef)
	if err := sh.RunV("cargo", "semver-checks", "check-release", "--baseline-rev", baseRef); err != nil {
		return fmt.Errorf("API compatibility check failed: breaking changes detected. Run 'mage updateAllowBreak' to proceed: %w", err)
	}
	fmt.Println("==> spicedb-rust: API compatible")
	return nil
}

// IntegrationTest starts SpiceDB via Docker, runs the #[ignore]d live tests,
// and then runs every example under examples/ against the same server.
//
// The example loop is the point of this target. Before it existed the body was
// `cargo test -- --ignored` alone, which ran the two ignored test functions and
// zero examples, while this comment claimed otherwise -- so every runtime
// assertion in all 13 examples was dead code that had never executed. See root
// DESIGN.md, "RULE: An example must be executed by CI and must be able to fail".
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

	// Start SpiceDB
	fmt.Println("==> Starting SpiceDB...")
	if err := sh.RunWithV(composeEnv, "docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	defer func() {
		fmt.Println("==> Stopping SpiceDB...")
		_ = sh.RunWithV(composeEnv, "docker", "compose", "-f", "docker-compose.test.yml", "down")
	}()

	// Wait for SpiceDB to be ready
	fmt.Println("==> Waiting for SpiceDB to be ready...")
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	// Fail before the slow part if the example set on disk is not the one this
	// runner expects to execute.
	if _, err := exampleTargets(); err != nil {
		return err
	}

	// The #[ignore]d live tests in tests/live_integration_test.rs. `--ignored`
	// is a filter, and libtest exits 0 when a filter selects nothing: drop the
	// #[ignore] attributes and this step runs zero tests, silently, green. So
	// the reported pass count is checked, exactly as
	// .github/workflows/rust.yaml does for its handshake test.
	clientEnv := map[string]string{"SPICEDB_ENDPOINT": endpoint, "SPICEDB_TOKEN": token}
	fmt.Println("==> Running live integration tests...")
	out, liveErr := sh.OutputWith(clientEnv, "cargo", "test", "--", "--ignored")
	fmt.Println(out)
	if liveErr != nil {
		return fmt.Errorf("integration tests failed: %w", liveErr)
	}
	if passed := libtestPassed(out); passed != wantLiveTestCount {
		return fmt.Errorf("live integration tests reported %d passed, want %d: "+
			"were the #[ignore] attributes in tests/live_integration_test.rs removed or renamed?",
			passed, wantLiveTestCount)
	}

	// Then the examples, which are the integration suite proper.
	return runExamples(clientEnv)
}

// CheckExamples verifies the example wiring without needing a server: that the
// glob still matches the expected number of files, and that every name in
// skippedExamples is an example that exists. It is cheap, so Test runs it too.
func CheckExamples() error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	fmt.Printf("==> spicedb-rust: %d examples on disk, %d skipped by the integration runner\n",
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
	files, err := filepath.Glob("examples/*.rs")
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(files)

	names := make([]string, 0, len(files))
	onDisk := make(map[string]bool, len(files))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".rs")
		names = append(names, name)
		onDisk[name] = true
	}

	if err := reconcile("examples/*.rs", names, onDisk); err != nil {
		return nil, err
	}
	return names, nil
}

// reconcile compares the example names found on disk against wantExamples in
// both directions, and checks that every skip target still exists.
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

// runExamples executes every example under examples/ except those listed in
// skippedExamples, and fails if the number executed is not the number expected.
func runExamples(env map[string]string) error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}

	var executed, failures []string
	for _, name := range names {
		if reason, skipped := skippedExamples[name]; skipped {
			fmt.Printf("==> SKIP %s (%s)\n", name, reason)
			continue
		}
		fmt.Printf("==> Running example: %s\n", name)
		if err := sh.RunWithV(env, "cargo", "run", "--quiet", "--example", name); err != nil {
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
		return fmt.Errorf("examples failed: %s", strings.Join(failures, ", "))
	}
	fmt.Printf("==> All %d examples passed (%d skipped).\n", len(executed), len(skippedExamples))
	return nil
}

// libtestPassed sums the "N passed" counts across every "test result:" line in
// libtest output. Each test binary prints its own line, so one filtered run
// produces several, almost all of them zero.
func libtestPassed(output string) int {
	total := 0
	for _, m := range libtestResult.FindAllStringSubmatch(output, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		total += n
	}
	return total
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
