//go:build mage

package main

import (
	"encoding/xml"
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
	protoClientDir = "../proto-clients/spicedb-python-proto"
	lastGenFile    = ".last-generation"

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which examples/conftest.py reads,
	// so the suite can be pointed at a SpiceDB on another port when 50051 is
	// taken.
	defaultEndpoint = "localhost:50051"
	defaultToken    = "somerandomkeyhere"

	// Number of files matching examples/*/test_*.py. A glob cannot list an
	// example that does not exist, but a renamed or moved file silently yields
	// a *shorter* list instead of an error, so the count is asserted rather
	// than trusted. Bump this when adding an example. See root DESIGN.md,
	// "RULE: An example must be executed by CI and must be able to fail",
	// clause 1.
	wantExampleCount = 19
)

// skippedExamples maps an example that IntegrationTest does not execute to the
// reason it does not. Every other example under examples/ MUST run. A skip has
// to be listed here to happen at all, so it is visible in the run's output and
// counted against wantExampleCount -- never the silent residue of a filter.
//
// This replaced `pytest -k "not watch"`. A `-k` substring filter that matches
// nothing exits 0, so the previous form could silently stop excluding (or
// silently start excluding everything) with no signal either way.
var skippedExamples = map[string]string{
	"watch_changes":      "open-ended stream; needs a bounded consumer with explicit cancellation",
	"sync_watch_changes": "open-ended stream; needs a bounded consumer with explicit cancellation",
}

// Gen updates the idiomatic Python client based on proto client changes.
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
			"Ensure all examples still work. Add new examples for new functionality. "+
			"Update DESIGN.md changelog if needed.",
		diff,
	)

	fmt.Println("==> Invoking Claude to update idiomatic client...")
	if err := runClaude(prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := sh.RunV("uv", "run", "pytest", "-v"); err == nil {
			fmt.Println("==> Tests passed!")
			head, _ := sh.Output("git", "rev-parse", "HEAD")
			_ = os.WriteFile(lastGenFile, []byte(strings.TrimSpace(head)), 0644)
			return nil
		}

		if attempt == maxRetries {
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

// Test runs the Python idiomatic client unit tests (no SpiceDB required).
// Integration tests (examples/) are run separately by IntegrationTest.
func Test() error {
	if err := CheckExamples(); err != nil {
		return err
	}
	return sh.RunV("uv", "run", "pytest", "tests/", "-v")
}

// CheckExamples verifies the example wiring without needing a server: that the
// glob still matches the expected number of example test files, and that every
// name the integration runner skips is an example that exists. It is cheap, so
// Test runs it too.
func CheckExamples() error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	fmt.Printf("==> spicedb-python: %d examples on disk, %d skipped by the integration runner\n",
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
	files, err := filepath.Glob("examples/*/test_*.py")
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(files)

	if len(files) != wantExampleCount {
		return nil, fmt.Errorf(
			"examples/*/test_*.py matched %d files, want %d: an example was added, renamed, or moved. "+
				"Update wantExampleCount in Magefile.go if the change is intended",
			len(files), wantExampleCount)
	}

	names := make([]string, 0, len(files))
	onDisk := make(map[string]bool, len(files))
	for _, f := range files {
		name := filepath.Base(filepath.Dir(f))
		if onDisk[name] {
			return nil, fmt.Errorf("example %q has more than one test_*.py file; "+
				"the runner assumes one per directory", name)
		}
		names = append(names, name)
		onDisk[name] = true
	}
	for name := range skippedExamples {
		if !onDisk[name] {
			return nil, fmt.Errorf("skippedExamples names %q, which is not an example on disk: "+
				"a renamed skip target would otherwise silently start being skipped by nothing", name)
		}
	}
	return names, nil
}

// Lint runs ruff check on all Python code.
func Lint() error {
	return sh.RunV("ruff", "check", ".")
}

// ApiCompat checks for breaking API changes against the given base git ref.
func ApiCompat(baseRef string) error {
	if _, err := exec.LookPath("griffe"); err != nil {
		return fmt.Errorf("griffe not found. Install with: uv tool install griffe")
	}

	fmt.Printf("==> Checking Python API compatibility against %s...\n", baseRef)
	if err := sh.RunV("griffe", "check", "spicedb", "-a", baseRef, "-b", "HEAD", "--search", "spicedb-python"); err != nil {
		return fmt.Errorf("API compatibility check failed: breaking changes detected. Run 'mage updateAllowBreak' to proceed: %w", err)
	}
	fmt.Println("==> spicedb-python: API compatible")
	return nil
}

// IntegrationTest starts SpiceDB via Docker and runs example integration tests.
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

	// Name the example files explicitly rather than filtering with `-k`: a
	// substring filter that matches nothing exits 0, so it can silently stop
	// selecting what it was written to select.
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	var paths, expected []string
	for _, name := range names {
		if reason, skipped := skippedExamples[name]; skipped {
			fmt.Printf("==> SKIP %s (%s)\n", name, reason)
			continue
		}
		paths = append(paths, filepath.Join("examples", name))
		expected = append(expected, name)
	}

	wantExecuted := wantExampleCount - len(skippedExamples)
	if len(expected) != wantExecuted {
		return fmt.Errorf("selected %d examples to run, want %d (%d on disk, %d skipped)",
			len(expected), wantExecuted, len(names), len(skippedExamples))
	}

	report, err := filepath.Abs(filepath.Join("build", "example-tests.xml"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(report), 0o755); err != nil {
		return err
	}
	defer func() { _ = os.Remove(report) }()

	fmt.Printf("==> Running %d example suites...\n", len(paths))
	args := append([]string{"run", "pytest", "-v", "--junitxml=" + report}, paths...)
	clientEnv := map[string]string{"SPICEDB_ENDPOINT": endpoint, "SPICEDB_TOKEN": token}
	runErr := sh.RunWithV(clientEnv, "uv", args...)

	// Check what actually ran before reporting on pass/fail: a suite that
	// collected nothing is a wiring failure, and pytest is happy to report a
	// green run over a subset.
	ran, total, err := junitExamplesRun(report)
	if err != nil {
		if runErr != nil {
			return fmt.Errorf("integration tests failed: %w", runErr)
		}
		return err
	}
	var missing []string
	for _, name := range expected {
		if !ran[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these examples were selected but contributed no test case: %s "+
			"(pytest reported %d test cases across %d of %d example suites)",
			strings.Join(missing, ", "), total, len(ran), len(expected))
	}

	if runErr != nil {
		return fmt.Errorf("integration tests failed: %w", runErr)
	}
	fmt.Printf("==> All %d example suites passed, %d test cases (%d skipped).\n",
		len(expected), total, len(skippedExamples))
	return nil
}

// junitExamplesRun reads a pytest JUnit XML report and returns the set of
// example directories that contributed at least one test case, plus the total
// number of test cases.
func junitExamplesRun(report string) (map[string]bool, int, error) {
	data, err := os.ReadFile(report)
	if err != nil {
		return nil, 0, fmt.Errorf("reading pytest report: %w", err)
	}
	var suites struct {
		Cases []struct {
			File      string `xml:"file,attr"`
			ClassName string `xml:"classname,attr"`
		} `xml:"testsuite>testcase"`
	}
	if err := xml.Unmarshal(data, &suites); err != nil {
		return nil, 0, fmt.Errorf("parsing pytest report: %w", err)
	}
	ran := make(map[string]bool)
	for _, c := range suites.Cases {
		// file is "examples/<name>/test_<name>.py"; classname is the same path
		// dotted, and is the fallback if a pytest version drops the attribute.
		src := c.File
		if src == "" {
			src = strings.ReplaceAll(c.ClassName, ".", "/")
		}
		parts := strings.Split(filepath.ToSlash(src), "/")
		if len(parts) >= 2 && parts[0] == "examples" {
			ran[parts[1]] = true
		}
	}
	return ran, len(suites.Cases), nil
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
