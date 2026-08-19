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
	if err := CheckExamples(); err != nil {
		return err
	}
	if err := sh.RunV("dotnet", "build", "SpiceDB.Client.sln"); err != nil {
		return err
	}
	if err := sh.RunV("dotnet", "test", "SpiceDB.Client.sln", "--no-build", "--verbosity", "normal"); err != nil {
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
	// Fail before starting anything if the examples on disk are not the ones
	// the solution builds.
	if err := CheckExamples(); err != nil {
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
	if err := sh.RunWithV(clientEnv, "dotnet", "test", "SpiceDB.Client.sln", "--verbosity", "normal"); err != nil {
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
	fmt.Println("==> Running examples against SpiceDB...")
	return sh.RunWithV(clientEnv, "dotnet", "test", examplesSolution, "--verbosity", "normal", "-maxcpucount:1")
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
