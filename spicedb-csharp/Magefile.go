//go:build mage

package main

import (
	"encoding/xml"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/authzed/spicedb-clients/internal/clauderun"
	"github.com/authzed/spicedb-clients/internal/gitlock"
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

	// Defaults for the container docker-compose.test.yml starts. Overridable
	// via SPICEDB_ENDPOINT / SPICEDB_TOKEN, which examples/SpiceDBTestServer.cs
	// reads, so the suite can be pointed at a SpiceDB on another port when
	// 50051 is taken.
	defaultEndpoint = "localhost:50051"
	defaultToken    = "somerandomkeyhere"
)

// slnProjectLine matches a solution's project entries:
//
//	Project("{TYPE-GUID}") = "Name", "relative\path.csproj", "{PROJECT-GUID}"
//
// Solution folders use the same syntax with a path that is not a .csproj, so
// the path is what distinguishes a real project from a folder.
var slnProjectLine = regexp.MustCompile(`^Project\("\{[^}]+\}"\) = "[^"]*", "([^"]+)", "(\{[^}]+\})"`)

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
// optionsInstruction is spelled out in the prompt rather than left to
// ./DESIGN.md because this exact mistake has been made twice by regeneration.
// Both times the upstream API gained an optional field, and both times it
// landed on a public signature: first as a trailing optional parameter, then --
// repairing that -- as a required positional parameter plus a compatibility
// overload. C# substitutes an optional parameter's default at the call site, so
// the first was binary-breaking for every already-compiled assembly, and the
// second broke callers outright.
const optionsInstruction = "A new option on an existing operation is a NEW PROPERTY on that " +
	"operation's options class (CheckOptions, LookupOptions), read by the existing " +
	"...WithOptionsAsync method. Do not add a parameter to any public method, optional or " +
	"otherwise: C# bakes an optional parameter's default into the call site, so adding one is a " +
	"binary-breaking change for every already-compiled assembly. Do not add a per-option method " +
	"variant either (...WithContextAsync and friends are what this replaces). If an operation has " +
	"no options class yet, add one modelled on CheckOptions and give the operation a " +
	"...WithOptionsAsync form beside its plain one; CancellationToken stays its own trailing " +
	"parameter. See root DESIGN.md, \"RULE: Every RPC wrapper must have one place to add an " +
	"option\", and ./DESIGN.md, \"Where a new option goes\"."

// changelogInstruction names CHANGELOG.md explicitly: the previous wording said
// "update DESIGN.md changelog", and the changelog is not in DESIGN.md.
const changelogInstruction = "Record what you changed in CHANGELOG.md -- not DESIGN.md -- under " +
	"the single existing \"## Unreleased\" heading. Never add a second one. Put the entry in the " +
	"right \"###\" subsection (Added, Changed, Fixed), formatted like the entries already there: " +
	"a bold \"**YYYY-MM-DD: one-line summary.**\" followed by an indented paragraph saying what " +
	"changed and why it matters to a caller. Update DESIGN.md itself only when the design changed, " +
	"not merely to note the change."

func Gen() error {
	// Read last generation baseline
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
			"Ensure all tests still pass.\n\n"+
			optionsInstruction+"\n\n"+
			changelogInstruction,
		stat, names, protoClientDir,
	)

	fmt.Println("==> Invoking Claude to update idiomatic client...")
	if err := clauderun.Run(prompt); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	// Test with retry loop
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("==> Running tests (attempt %d/%d)...\n", attempt, maxRetries)

		// Build + run tests
		if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
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

		if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--no-build", "--verbosity", "normal"); err == nil {
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
	if err := CheckExamples(); err != nil {
		return err
	}
	if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
		return err
	}
	// The TLS handshake test is excluded here and run in its own CI step: it needs
	// the network, and keeping it out of the default run is the gate root DESIGN.md's
	// "RULE: A system-TLS constructor must reach a real server" clause 3 asks for.
	// xunit 2 has no runtime skip, so a trait is the only gate that cannot report a
	// test as passed while it did nothing.
	if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--no-build", "--verbosity", "normal",
		"--filter", "Category!=TlsHandshake"); err != nil {
		return err
	}
	return sh.RunV("dotnet", "build", examplesSolution)
}

// CheckExamples reconciles the example projects on disk against the ones the
// examples solution lists, in both directions, and fails on any divergence.
//
// This is the guard the original defect had none of. All twelve examples once
// sat outside every solution file, so `dotnet build`/`dotnet test` never saw
// them -- for the repo's entire history. Adding SpiceDB.Client.Examples.sln
// fixed the instance, but a solution is a hand-maintained snapshot: example #14
// reintroduces the same defect by default, because nothing compares the two.
// It also checks that every listed project has build configurations, since a
// project can be listed and still excluded from every build.
//
// It needs no server, so Test runs it too. See root DESIGN.md, "RULE: An
// example must be executed by CI and must be able to fail", clause 1.
func CheckExamples() error {
	onDisk, err := filepath.Glob("examples/*/*.csproj")
	if err != nil {
		return fmt.Errorf("glob examples failed: %w", err)
	}
	sort.Strings(onDisk)

	inSolution, configured, err := solutionProjects(examplesSolution)
	if err != nil {
		return err
	}

	diskSet := make(map[string]bool, len(onDisk))
	for _, p := range onDisk {
		diskSet[filepath.ToSlash(p)] = true
	}

	var missing, phantom []string
	for p := range diskSet {
		if _, listed := inSolution[p]; !listed {
			missing = append(missing, p)
		}
	}
	for p := range inSolution {
		// The library project is legitimately in the examples solution -- the
		// examples reference it. Only examples/ entries are reconciled here.
		if !strings.HasPrefix(p, "examples/") {
			continue
		}
		if !diskSet[p] {
			phantom = append(phantom, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(phantom)

	if len(missing) > 0 {
		return fmt.Errorf("these example projects exist on disk but are not in %s, so nothing "+
			"builds or runs them: %s. Add them with: dotnet sln %s add <path>",
			examplesSolution, strings.Join(missing, ", "), examplesSolution)
	}
	if len(phantom) > 0 {
		return fmt.Errorf("%s lists these example projects, which are not on disk: %s",
			examplesSolution, strings.Join(phantom, ", "))
	}

	var unbuilt []string
	for p := range inSolution {
		if !strings.HasPrefix(p, "examples/") {
			continue
		}
		if !configured[inSolution[p]] {
			unbuilt = append(unbuilt, p)
		}
	}
	sort.Strings(unbuilt)
	if len(unbuilt) > 0 {
		return fmt.Errorf("these example projects are listed in %s but have no Build.0 "+
			"configuration, so the solution loads them without building them: %s",
			examplesSolution, strings.Join(unbuilt, ", "))
	}

	fmt.Printf("==> spicedb-csharp: %d example projects on disk, all present and buildable in %s\n",
		len(onDisk), examplesSolution)
	return nil
}

// exampleProjectNames returns the assembly names of the example projects on
// disk -- the .csproj base names, which are also the built .dll names.
func exampleProjectNames() ([]string, error) {
	paths, err := filepath.Glob("examples/*/*.csproj")
	if err != nil {
		return nil, fmt.Errorf("glob examples failed: %w", err)
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".csproj"))
	}
	sort.Strings(names)
	return names, nil
}

// solutionProjects parses a .sln and returns its real projects as a map from
// slash-separated relative path to project GUID, plus the set of GUIDs that
// have at least one Build.0 configuration entry.
func solutionProjects(solution string) (map[string]string, map[string]bool, error) {
	data, err := os.ReadFile(solution)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", solution, err)
	}
	projects := make(map[string]string)
	configured := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if m := slnProjectLine.FindStringSubmatch(line); m != nil {
			path := strings.ReplaceAll(m[1], `\`, "/")
			// Solution folders reuse the Project(...) syntax with a plain name
			// in place of a project path.
			if strings.HasSuffix(strings.ToLower(path), ".csproj") {
				projects[path] = m[2]
			}
			continue
		}
		if strings.Contains(line, ".Build.0 =") {
			if i := strings.Index(line, "}"); i > 0 {
				configured[line[:i+1]] = true
			}
		}
	}
	if len(projects) == 0 {
		return nil, nil, fmt.Errorf("%s lists no projects; is the format understood?", solution)
	}
	return projects, configured, nil
}

// Lint runs dotnet format to check code style, over both solutions.
//
// The examples solution is included because it was not: `dotnet format` ran
// against SpiceDB.Client.sln alone, so no example was ever linted.
func Lint() error {
	if err := sh.RunV("dotnet", "format", "SpiceDB.Client.sln", "--verify-no-changes"); err != nil {
		return err
	}
	return sh.RunV("dotnet", "format", examplesSolution, "--verify-no-changes")
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
		_ = gitRun("-C", "..", "worktree", "remove", "--force", worktreeDir)
		_ = os.RemoveAll(worktreeDir)
	}()

	if err := gitRunV("-C", "..", "worktree", "add", worktreeDir, baseRef); err != nil {
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
	// Fail before starting anything if the examples on disk are not the ones
	// the solution builds.
	if err := CheckExamples(); err != nil {
		return err
	}
	projects, err := exampleProjectNames()
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

	clientEnv := map[string]string{"SPICEDB_ENDPOINT": endpoint, "SPICEDB_TOKEN": token}

	// Run integration tests (no --no-build: VSTest cannot load .NET 10 assemblies
	// without a fresh build step, and the build is fast enough to not matter here)
	fmt.Println("==> Running integration tests...")
	// See Test(): the TLS handshake test has its own CI step and does not belong in a
	// run whose point is the local SpiceDB container.
	if err := sh.RunWithV(clientEnv, "dotnet", "test", "SpiceDB.Client.sln", "--verbosity", "normal",
		"--filter", "Category!=TlsHandshake"); err != nil {
		return err
	}

	// Then the examples, which are the integration suite proper -- each one
	// drives a real SpiceDB. They live in their own solution because they
	// cannot run in the unit job, which has no server.
	//
	// -maxcpucount:1 because all thirteen projects share that one SpiceDB and
	// each writes a whole schema. Run concurrently, one project's WriteSchema
	// lands between another's schema write and its relationship write, and the
	// second fails with "relation/permission `editor` not found under
	// definition `document`" -- nondeterministically, on a different project
	// each run. Two consecutive parallel runs here failed on four different
	// examples. The examples are cheap; correctness is worth the seconds.
	//
	// The run is TRX-logged into a directory cleared first, so what follows can
	// assert what actually executed. `dotnet test` over a solution *prints*
	// "No test is available in ..." for an assembly with no tests and still
	// exits 0 -- so commenting out the single [Fact] in RelationshipCounters,
	// which leaves the file, the .csproj and the .sln entry all in place and
	// therefore passes CheckExamples, was silently green before this.
	results, err := filepath.Abs(filepath.Join("build", "example-results"))
	if err != nil {
		return err
	}
	if err := os.RemoveAll(results); err != nil {
		return fmt.Errorf("clearing %s failed: %w", results, err)
	}

	fmt.Println("==> Running examples against SpiceDB...")
	runErr := sh.RunWithV(clientEnv, "dotnet", "test", examplesSolution,
		"--verbosity", "normal", "-maxcpucount:1",
		"--logger", "trx", "--results-directory", results)

	ran, total, err := trxAssembliesRun(results)
	if err != nil {
		if runErr != nil {
			return runErr
		}
		return err
	}
	var silent []string
	for _, name := range projects {
		if !ran[strings.ToLower(name)] {
			silent = append(silent, name)
		}
	}
	sort.Strings(silent)
	if len(silent) > 0 {
		return fmt.Errorf("these example projects executed no test: %s "+
			"(dotnet reported %d executed tests across %d of %d example assemblies)",
			strings.Join(silent, ", "), total, len(ran), len(projects))
	}

	if runErr != nil {
		return runErr
	}
	fmt.Printf("==> All %d example projects ran, %d tests.\n", len(projects), total)
	return nil
}

// trxAssembliesRun reads the TRX reports `dotnet test` wrote and returns the
// set of example assembly names (without ".dll") that executed at least one
// test, plus how many tests executed in total.
//
// A "NotExecuted" outcome is what VSTest records for a [Fact(Skip = "...")],
// so it does not count: a fully-skipped project must not satisfy the assertion
// that it ran.
func trxAssembliesRun(dir string) (map[string]bool, int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.trx"))
	if err != nil {
		return nil, 0, fmt.Errorf("glob TRX reports failed: %w", err)
	}
	if len(files) == 0 {
		return nil, 0, fmt.Errorf("no TRX reports under %s: the example run produced no "+
			"results, so nothing can be said about what executed", dir)
	}

	ran := make(map[string]bool)
	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", f, err)
		}
		var run struct {
			Definitions []struct {
				ID      string `xml:"id,attr"`
				Storage string `xml:"storage,attr"`
			} `xml:"TestDefinitions>UnitTest"`
			Results []struct {
				TestID  string `xml:"testId,attr"`
				Outcome string `xml:"outcome,attr"`
			} `xml:"Results>UnitTestResult"`
		}
		if err := xml.Unmarshal(data, &run); err != nil {
			return nil, 0, fmt.Errorf("parsing %s: %w", f, err)
		}
		assembly := make(map[string]string, len(run.Definitions))
		for _, d := range run.Definitions {
			// VSTest lowercases the whole storage path in the TRX, so the
			// assembly name is matched case-insensitively against the project
			// names rather than compared directly.
			path := strings.ReplaceAll(d.Storage, `\`, "/")
			assembly[d.ID] = strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".dll"))
		}
		for _, r := range run.Results {
			if r.Outcome == "NotExecuted" {
				continue
			}
			if name := assembly[r.TestID]; name != "" {
				ran[name] = true
				total++
			}
		}
	}
	return ran, total, nil
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
