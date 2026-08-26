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
	"caveated_check",
	"check_permission",
	"custom_tls",
	"delete_relationships",
	"error_mapping",
	"expand_permission_tree",
	"insecure_opt_in",
	"lookup_resources",
	"lookup_subjects",
	"raw_escape_hatch",
	"read_relationships",
	"read_your_writes",
	"retry_policy",
	"schema_management",
	"sync_check_permission",
	"sync_read_relationships",
	"sync_watch_changes",
	"sync_write_relationships",
	"unrepresentable_values",
	"watch_changes",
	"write_relationships",
}

// skippedExamples maps an example that IntegrationTest does not execute to the
// reason it does not. Every other example under examples/ MUST run. A skip has
// to be listed here to happen at all, so it is visible in the run's output and
// counted against wantExamples -- never the silent residue of a filter.
//
// This replaced `pytest -k "not watch"`. A `-k` substring filter that matches
// nothing exits 0, so the previous form could silently stop excluding (or
// silently start excluding everything) with no signal either way.
//
// It is empty on purpose. watch_changes and sync_watch_changes used to live
// here -- "open-ended stream; needs a bounded consumer with explicit
// cancellation" -- which meant the only streaming examples never ran, and root
// DESIGN.md's "RULE: Abandoning a stream must release it" had no executed
// coverage at all. Both now have that bounded consumer, so nothing is skipped.
var skippedExamples = map[string]string{}

// claudeAvailable returns true if the claude CLI is installed and usable.
// Returns false when running in CI (CI env var set) because the claude binary
// may be present but not authenticated.
//
// CI_REGENERATION is the one exception, and it is set by exactly one workflow:
// .github/workflows/regen-from-api.yaml, which is also the only workflow holding
// Claude credentials. Every other CI job -- notably meta.yaml's gen-nodiff --
// must keep taking the false branch here, or `mage gen:all` starts making
// unreviewed changes inside a check whose whole purpose is asserting that
// generation produces no diff.
func claudeAvailable() bool {
	if os.Getenv("CI") != "" && os.Getenv("CI_REGENERATION") == "" {
		return false
	}
	_, err := exec.LookPath("claude")
	return err == nil
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

	if !claudeAvailable() {
		fmt.Println("==> claude not available; skipping idiomatic client update (gen-nodiff mode).")
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
	if err := reconcile("examples/*/test_*.py", names, onDisk); err != nil {
		return nil, err
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

	wantExecuted := len(wantExamples) - len(skippedExamples)
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
	fmt.Printf("==> All %d example suites passed: %d test cases executed; %s skipped.\n",
		len(expected), total, plural(len(skippedExamples), "example"))
	return nil
}

// junitExamplesRun reads a pytest JUnit XML report and returns the set of
// example directories that contributed at least one *executed* test case, plus
// how many were executed. Skipped cases do not count: a skip is still reported
// as a case, so counting it would let a fully-skipped example satisfy the
// assertion that it ran.
func junitExamplesRun(report string) (map[string]bool, int, error) {
	data, err := os.ReadFile(report)
	if err != nil {
		return nil, 0, fmt.Errorf("reading pytest report: %w", err)
	}
	var suites struct {
		Cases []struct {
			File      string    `xml:"file,attr"`
			ClassName string    `xml:"classname,attr"`
			Skipped   *struct{} `xml:"skipped"`
		} `xml:"testsuite>testcase"`
	}
	if err := xml.Unmarshal(data, &suites); err != nil {
		return nil, 0, fmt.Errorf("parsing pytest report: %w", err)
	}
	ran := make(map[string]bool)
	executed := 0
	for _, c := range suites.Cases {
		// A skipped case is reported like any other, so a @pytest.mark.skip
		// would otherwise satisfy "this example contributed a test case" while
		// executing nothing.
		if c.Skipped != nil {
			continue
		}
		executed++
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
	return ran, executed, nil
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

// plural renders "1 example" / "2 examples" so the summary line stays readable
// when exactly one example is skipped.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
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

// claudeArgs returns the CLI arguments for a Claude invocation.
//
// Under CI_REGENERATION -- set only by .github/workflows/regen-from-api.yaml,
// the one workflow holding Claude credentials -- two flags are required that
// local runs must not get:
//
//	--print                              a CI runner has no TTY; the bare
//	                                     interactive form has never run there
//	--permission-mode bypassPermissions  without it Claude replies "I don't
//	                                     have permission to write this file",
//	                                     edits nothing, and still exits 0 --
//	                                     so a regeneration reports success and
//	                                     produces an empty PR (observed on
//	                                     runs 33010304938 vs 33010790114)
//
// The bypass is confined to that workflow, which runs on an ephemeral
// single-purpose container. Local runs keep normal interactive prompting.
func claudeArgs() []string {
	if os.Getenv("CI_REGENERATION") == "" {
		return nil
	}
	return []string{"--print", "--permission-mode", "bypassPermissions"}
}

// runClaude pipes the prompt to claude via stdin so output streams in real time.
func runClaude(prompt string) error {
	cmd := exec.Command("claude", claudeArgs()...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
