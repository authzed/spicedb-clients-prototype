//go:build mage

package main

import (
	"encoding/xml"
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
	protoClientDir = "../proto-clients/spicedb-java-proto"
	lastGenFile    = ".last-generation"

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which SpiceDBIntegrationTest reads,
	// so the suite can be pointed at a SpiceDB on another port when 50051 is
	// taken.
	defaultEndpoint = "localhost:50051"
	defaultToken    = "somerandomkeyhere"

	exampleTestDir = "examples/src/test/java/com/authzed/spicedb/examples"
)

// wantExamples is every example test class this runner expects in
// exampleTestDir. The set is pinned rather than counted: a count alone still
// passes when a class is renamed, and a glob cannot list a class that is not
// there, so a moved or renamed example would otherwise just shrink the run and
// still report green. Add a name here when adding an example. See root
// DESIGN.md, "RULE: An example must be executed by CI and must be able to
// fail", clause 1.
var wantExamples = []string{
	"BulkOperationsTest",
	"CallDeadlinesTest",
	"CheckPermissionTest",
	"ConditionalCheckTest",
	"ErrorMappingTest",
	"ExpandPermissionTreeTest",
	"InsecureOptInTest",
	"LookupResourcesTest",
	"LookupSubjectsTest",
	"RawEscapeHatchTest",
	"ReadRelationshipsTest",
	"RelationshipCountersTest",
	"RetryPolicyTest",
	"SchemaManagementTest",
	"SchemaReflectionTest",
	"UnrepresentableValuesTest",
	"WatchChangesTest",
	"WriteRelationshipsTest",
}

// notAnExample lists files under exampleTestDir that are not themselves
// examples. SpiceDBIntegrationTest is the abstract base class the examples
// extend: it has no @Test method, so JUnit never reports it as having run and
// it must not be counted as one.
var notAnExample = map[string]bool{
	"SpiceDBIntegrationTest": true,
}

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

// gitRunV runs `git <args...>` under the repository-wide gitlock with
// streamed stdout/stderr, exactly as sh.RunV would, only serialized against
// the other languages generating concurrently.
func gitRunV(args ...string) error {
	return gitlock.Do(func() error {
		return sh.RunV("git", args...)
	})
}

// Gen updates the idiomatic client based on proto client changes.
func Gen() error {
	// Read last generation baseline
	baseline, err := os.ReadFile(lastGenFile)
	if err != nil {
		baseline = []byte("HEAD~1")
	}

	// Summarize rather than paste. spicedb-java-proto alone produced 263 changed
	// files and 56,401 changed lines in one regeneration -- 55x the next largest
	// client -- and pasting that raw diff exceeded the context window outright
	// ("Prompt is too long", run 33014735194), failing java on every run. Claude
	// has file access on the runner, so a stat summary plus a name-status list is
	// smaller AND more useful: it can read whichever files it actually needs.
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
			"Ensure all examples still compile and pass. "+
			"Add new examples for new functionality. "+
			"Update DESIGN.md changelog if needed.",
		stat, names, protoClientDir,
	)

	fmt.Println("==> Invoking Claude to update idiomatic client...")
	if err := clauderun.Run(prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)

		if err := sh.RunV("gradle", "build"); err != nil {
			if attempt == maxRetries {
				if err := gitRun("checkout", "--", "."); err != nil {
					return fmt.Errorf("build failed after %d retries, and rollback (git checkout) also failed: %w", maxRetries, err)
				}
				return fmt.Errorf("build failed after %d retries", maxRetries)
			}
			fmt.Println("==> Build failed, asking Claude to fix...")
			if err := clauderun.Run("Build failed. Read the build output above and fix the issues."); err != nil {
				return fmt.Errorf("claude fix invocation failed: %w", err)
			}
			continue
		}

		if err := sh.RunV("gradle", "test"); err == nil {
			fmt.Println("==> Tests passed!")
			// Update baseline
			head, _ := gitOutput("rev-parse", "HEAD")
			_ = os.WriteFile(lastGenFile, []byte(strings.TrimSpace(head)), 0644)
			return nil
		}

		if attempt == maxRetries {
			fmt.Printf("==> Tests failed after %d attempts. Rolling back.\n", maxRetries)
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

// Test runs the lib unit tests via Gradle. Examples are integration tests
// that need a live SpiceDB; they run via IntegrationTest, which starts
// docker-compose first.
func Test() error {
	if err := CheckExamples(); err != nil {
		return err
	}
	return sh.RunV("gradle", ":lib:test")
}

// CheckExamples verifies the example wiring without needing a server or Gradle:
// that the expected number of example test classes are on disk. It is cheap, so
// Test runs it too.
func CheckExamples() error {
	names, err := exampleTargets()
	if err != nil {
		return err
	}
	fmt.Printf("==> spicedb-java: %d example test classes on disk\n", len(names))
	return nil
}

// exampleTargets returns the sorted example test-class names on disk, after
// asserting the set is the one this runner expects.
//
// The count assertion is what makes a rename fail loudly: a glob cannot list an
// example that is not there, so without it a moved or renamed file just shrinks
// the run and still reports green.
func exampleTargets() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(exampleTestDir, "*Test.java"))
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(files)

	var names []string
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".java")
		if notAnExample[name] {
			continue
		}
		names = append(names, name)
	}

	want := make(map[string]bool, len(wantExamples))
	for _, name := range wantExamples {
		want[name] = true
	}
	onDisk := make(map[string]bool, len(names))
	for _, name := range names {
		onDisk[name] = true
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
		return nil, fmt.Errorf(
			"%s/*Test.java does not match wantExamples in Magefile.go -- expected but absent: "+
				"[%s]; present but not expected: [%s]. Update wantExamples if the change is intended",
			exampleTestDir, strings.Join(missing, ", "), strings.Join(unexpected, ", "))
	}
	return names, nil
}

// junitClassesRun reads Gradle's JUnit XML reports and returns the set of
// simple test-class names that reported at least one *executed* test case,
// plus how many were executed. Skipped (@Disabled) cases do not count.
func junitClassesRun(resultsDir string) (map[string]bool, int, error) {
	files, err := filepath.Glob(filepath.Join(resultsDir, "TEST-*.xml"))
	if err != nil {
		return nil, 0, fmt.Errorf("glob test results failed: %w", err)
	}
	if len(files) == 0 {
		return nil, 0, fmt.Errorf("no JUnit reports under %s: the example task produced no "+
			"results, so nothing can be said about what ran", resultsDir)
	}
	ran := make(map[string]bool)
	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", f, err)
		}
		var suite struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Skipped *struct{} `xml:"skipped"`
			} `xml:"testcase"`
		}
		if err := xml.Unmarshal(data, &suite); err != nil {
			return nil, 0, fmt.Errorf("parsing %s: %w", f, err)
		}
		executed := 0
		for _, c := range suite.Cases {
			// A @Disabled case is still reported as a testcase, so counting it
			// would let a fully-disabled class satisfy the assertion that it
			// ran.
			if c.Skipped == nil {
				executed++
			}
		}
		if executed == 0 {
			continue
		}
		simple := suite.Name
		if i := strings.LastIndex(simple, "."); i >= 0 {
			simple = simple[i+1:]
		}
		ran[simple] = true
		total += executed
	}
	return ran, total, nil
}

// Build compiles all source via Gradle.
func Build() error {
	return sh.RunV("gradle", "build")
}

// Lint runs Spotless check for code formatting.
func Lint() error {
	return sh.RunV("gradle", "spotlessCheck")
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

	// Build current version (assemble only — skip tests, we just need the JAR)
	if err := sh.RunV("gradle", "assemble"); err != nil {
		return fmt.Errorf("current build failed: %w", err)
	}

	currentJARs, err := filepath.Glob("lib/build/libs/lib-*.jar")
	if err != nil || len(currentJARs) == 0 {
		return fmt.Errorf("current JAR not found in lib/build/libs/")
	}
	currentJAR, _ := filepath.Abs(currentJARs[0])

	// Create a temporary worktree at the baseline ref.
	// MkdirTemp creates the directory, but git worktree add needs it to not exist,
	// so we remove it first and let git recreate it.
	worktreeDir, err := os.MkdirTemp("", "spicedb-java-baseline-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	_ = os.Remove(worktreeDir)
	defer func() {
		_ = gitRun("-C", "..", "worktree", "remove", "--force", worktreeDir)
		_ = os.RemoveAll(worktreeDir)
	}()

	if err := gitRunV("-C", "..", "worktree", "add", worktreeDir, baseRef); err != nil {
		return fmt.Errorf("git worktree add failed: %w", err)
	}

	// Build baseline in the worktree
	baselineDir := filepath.Join(worktreeDir, "spicedb-java")
	if err := sh.RunV("gradle", "-p", baselineDir, ":lib:assemble"); err != nil {
		return fmt.Errorf("baseline build failed: %w", err)
	}

	baselineJARs, _ := filepath.Glob(filepath.Join(baselineDir, "lib", "build", "libs", "lib-*.jar"))
	if len(baselineJARs) == 0 {
		return fmt.Errorf("baseline JAR not found in worktree")
	}
	baselineJAR := baselineJARs[0]

	japicmpJar, _ = filepath.Abs(japicmpJar)

	// Compare: -o = old (baseline), -n = new (current).
	// --error-on-binary-incompatibility is what makes this a gate: without it japicmp
	// prints the incompatibilities it finds and still exits 0, so sh.RunV returns nil
	// and this reports "API compatible" regardless of what was found.
	if err := sh.RunV("java", "-jar", japicmpJar, "-o", baselineJAR, "-n", currentJAR,
		"--only-incompatible", "--ignore-missing-classes",
		"--error-on-binary-incompatibility"); err != nil {
		return fmt.Errorf("API compatibility check failed: breaking changes detected "+
			"(listed above). If the break is intentional, run 'mage updateAllowBreak' "+
			"from the repository root -- it is a root-level target, not one of this "+
			"module's: %w", err)
	}
	fmt.Println("==> spicedb-java: API compatible")
	return nil
}

// IntegrationTest starts SpiceDB via Docker and runs examples against it.
func IntegrationTest() error {
	// Fail before starting anything if the example set on disk is not the one
	// this runner expects to execute.
	names, err := exampleTargets()
	if err != nil {
		return err
	}

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

	// Clear the example reports first. The part that carries the weight is
	// what this makes true afterwards: any report present was written by *this*
	// run, so the check below is evidence rather than decoration, and its
	// absence is itself a failure ("no JUnit reports under ...").
	//
	// Deleting the outputs usually also makes :examples:test out of date, but
	// that is the weaker of the two reasons and cannot be relied on: a warm
	// daemon with stale file-watching has been observed reporting
	// ":examples:test UP-TO-DATE" with this directory freshly deleted. The run
	// still failed correctly -- on the missing reports, not on the staleness.
	// Do not remove the report-presence check as redundant.
	exampleResults := "examples/build/test-results/test"
	if err := os.RemoveAll(exampleResults); err != nil {
		return fmt.Errorf("clearing %s failed: %w", exampleResults, err)
	}

	// Run integration tests via Gradle. `gradle test` covers :lib:test and
	// :examples:test; the examples are the integration suite proper.
	fmt.Println("==> Running integration tests...")
	clientEnv := map[string]string{"SPICEDB_ENDPOINT": endpoint, "SPICEDB_TOKEN": token}
	runErr := sh.RunWithV(clientEnv, "gradle", "test")

	// Check what actually ran before reporting on pass/fail. Gradle skips a
	// task it believes is up to date and reports the build as successful, so a
	// green `gradle test` on its own does not establish that any example
	// executed.
	ran, total, err := junitClassesRun(exampleResults)
	if err != nil {
		if runErr != nil {
			return fmt.Errorf("integration tests failed: %w", runErr)
		}
		return err
	}
	var missing []string
	for _, name := range names {
		if !ran[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these example test classes reported no test case: %s "+
			"(Gradle reported %d tests across %d of %d example classes)",
			strings.Join(missing, ", "), total, len(ran), len(names))
	}

	if runErr != nil {
		return fmt.Errorf("integration tests failed: %w", runErr)
	}
	fmt.Printf("==> All %d example classes ran, %d tests.\n", len(names), total)
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
			// Extra delay for gRPC server to finish initialization after port is open
			time.Sleep(3 * time.Second)
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("SpiceDB not ready at %s after %s", addr, timeout)
}
