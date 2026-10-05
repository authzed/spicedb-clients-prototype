//go:build mage

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/authzed/spicedb-clients/internal/clauderun"
	"github.com/authzed/spicedb-clients/internal/gitlock"
	"github.com/magefile/mage/sh"
)

const (
	maxRetries     = 3
	protoClientDir = "../proto-clients/spicedb-elixir-proto"
	lastGenFile    = ".last-generation"

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which examples/support/example_case.ex
	// reads, so the suite can be pointed at a SpiceDB on another port when
	// 50051 is taken.
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
	"relationship_counters",
	"retry_policy",
	"roaring_lookup_resources",
	"schema_management",
	"schema_reflection",
	"unrepresentable_values",
	"watch_changes",
	"write_relationships",
}

// skippedExamples maps an example that IntegrationTest does not execute to the
// reason it does not. Every other example under examples/ MUST run. A skip has
// to be listed here to happen at all, so it is visible in the run's output and
// counted against wantExamples, never the silent residue of a filter. It is
// empty on purpose.
var skippedExamples = map[string]string{}

// gitOutput runs `git <args...>` under the repository-wide gitlock and
// returns its output exactly as sh.Output would, only serialized against the
// other languages generating concurrently.
func gitOutput(args ...string) (string, error) {
	var out string
	var outErr error
	if err := gitlock.Do(func() error {
		out, outErr = sh.Output("git", args...)
		return nil
	}); err != nil {
		return "", err
	}
	return out, outErr
}

// gitRun runs `git <args...>` under the repository-wide gitlock, exactly as
// sh.Run would, only serialized against the other languages generating
// concurrently. Unlike the sh.Run calls this replaces, its error must not be
// discarded: a failed rollback leaves this language's half-generated output
// in the tree for the root Magefile's commitIfChanged to sweep up.
func gitRun(args ...string) error {
	return gitlock.Do(func() error {
		return sh.Run("git", args...)
	})
}

// Gen updates the idiomatic Elixir client based on proto client changes.
func Gen() error {
	baseline, err := os.ReadFile(lastGenFile)
	if err != nil {
		baseline = []byte("HEAD~1")
	}

	// Summarize rather than paste, so a large proto diff cannot blow the context
	// window. See spicedb-java/Magefile.go for the measured cause and numbers.
	stat, err := gitOutput("diff", "--stat", strings.TrimSpace(string(baseline)), "--", protoClientDir)
	if err != nil {
		stat, _ = gitOutput("diff", "--stat", "HEAD~1", "--", protoClientDir)
	}
	names, err := gitOutput("diff", "--name-status", strings.TrimSpace(string(baseline)), "--", protoClientDir)
	if err != nil {
		names, _ = gitOutput("diff", "--name-status", "HEAD~1", "--", protoClientDir)
	}

	if strings.TrimSpace(names) == "" {
		fmt.Println("==> No proto changes detected, skipping.")
		return nil
	}

	if !clauderun.Available() {
		fmt.Println("==> claude not available; skipping idiomatic client update (gen-nodiff mode).")
		return nil
	}

	prompt := fmt.Sprintf(
		"The proto client has changed.\n\nSummary of changes:\n\n%s\n\nChanged files:\n\n%s\n\n"+
			"Read the changed files under %s for the details you need. "+
			"Read ../DESIGN.md and ./DESIGN.md. Update this client accordingly. "+
			"Ensure all examples still work. Add new examples for new functionality.\n\n"+
			clauderun.ChangelogInstruction,
		stat, names, protoClientDir,
	)

	fmt.Println("==> Invoking Claude to update idiomatic client...")
	if err := clauderun.Run(prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)
		if err := Test(); err == nil {
			fmt.Println("==> Tests passed!")
			head, _ := gitOutput("rev-parse", "HEAD")
			_ = os.WriteFile(lastGenFile, []byte(strings.TrimSpace(head)), 0644)
			return nil
		}

		if attempt == maxRetries {
			if err := gitRun("checkout", "--", "."); err != nil {
				return fmt.Errorf("tests failed after %d retries, and rollback (git checkout) also failed: %w", maxRetries, err)
			}
			return fmt.Errorf("tests failed after %d retries", maxRetries)
		}

		fmt.Println("==> Tests failed, asking Claude to fix...")
		if err := clauderun.Run("Tests failed. Read the test output above and fix the issues."); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}
	}

	return nil
}

// Test checks the example wiring, compiles with warnings as errors, and runs
// the unit tests in test/, which use a fake transport and need no server.
func Test() error {
	if err := CheckExamples(); err != nil {
		return err
	}
	testEnv := map[string]string{"MIX_ENV": "test"}
	if err := sh.RunWithV(testEnv, "mix", "compile", "--warnings-as-errors"); err != nil {
		return err
	}
	return sh.RunWithV(testEnv, "mix", "test")
}

// CheckExamples verifies the example wiring without needing a server: that the
// glob still matches the expected number of example specs, and that every name
// the integration runner skips is an example that exists. It is cheap, so Test
// runs it too.
func CheckExamples() error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	fmt.Printf("==> spicedb-elixir: %d examples on disk, %d skipped by the integration runner\n",
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
	files, err := filepath.Glob("examples/*/*_test.exs")
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(files)

	names := make([]string, 0, len(files))
	onDisk := make(map[string]bool, len(files))
	for _, f := range files {
		name := filepath.Base(filepath.Dir(f))
		if onDisk[name] {
			return nil, fmt.Errorf("example %q has more than one _test.exs file; "+
				"the runner assumes one per directory", name)
		}
		names = append(names, name)
		onDisk[name] = true
	}
	if err := reconcile("examples/*/*_test.exs", names, onDisk); err != nil {
		return nil, err
	}
	return names, nil
}

// Lint checks formatting, runs credo in strict mode, and runs dialyzer.
func Lint() error {
	if err := sh.RunV("mix", "format", "--check-formatted"); err != nil {
		return err
	}
	if err := sh.RunV("mix", "credo", "--strict"); err != nil {
		return err
	}
	return sh.RunV("mix", "dialyzer")
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

	// Name the example directories explicitly rather than filtering by tag: a
	// filter that matches nothing exits 0, so it can silently stop selecting
	// what it was written to select.
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

	report, err := filepath.Abs(filepath.Join("build", "example-tests.json"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(report), 0o755); err != nil {
		return err
	}
	_ = os.Remove(report)
	defer func() { _ = os.Remove(report) }()

	fmt.Printf("==> Running %d examples...\n", len(paths))
	args := append([]string{"test"}, paths...)
	clientEnv := map[string]string{
		"MIX_ENV":                "test",
		"SPICEDB_EXAMPLES":       "1",
		"SPICEDB_EXAMPLE_REPORT": report,
		"SPICEDB_ENDPOINT":       endpoint,
		"SPICEDB_TOKEN":          token,
	}
	runErr := sh.RunWithV(clientEnv, "mix", args...)

	// Check what actually ran before reporting on pass/fail: an example that
	// contributed nothing is a wiring failure, and ExUnit is happy to report
	// a green run over an empty selection.
	ran, total, err := examplesRun(report)
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
		return fmt.Errorf("these examples were selected but executed no test: %s "+
			"(ExUnit reported %d tests across %d of %d examples)",
			strings.Join(missing, ", "), total, len(ran), len(expected))
	}

	if runErr != nil {
		return fmt.Errorf("integration tests failed: %w", runErr)
	}
	fmt.Printf("==> All %d examples passed: %d tests executed; %s skipped.\n",
		len(expected), total, plural(len(skippedExamples), "example"))
	return nil
}

// examplesRun reads the JSON report examples/support/report_formatter.ex
// writes and returns the set of example directories that contributed at
// least one executed test, plus how many were executed. Skipped and excluded
// tests do not count: they are still reported, so counting them would let a
// fully-skipped example satisfy the assertion that it ran.
func examplesRun(report string) (map[string]bool, int, error) {
	data, err := os.ReadFile(report)
	if err != nil {
		return nil, 0, fmt.Errorf("reading example report: %w", err)
	}
	var parsed struct {
		Tests []struct {
			File   string `json:"file"`
			Status string `json:"status"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, 0, fmt.Errorf("parsing example report: %w", err)
	}
	ran := make(map[string]bool)
	executed := 0
	for _, t := range parsed.Tests {
		if t.Status == "skipped" {
			continue
		}
		executed++
		// file is "examples/<name>/<name>_test.exs".
		parts := strings.Split(filepath.ToSlash(t.File), "/")
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
