//go:build mage

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
)

const (
	maxRetries = 3
)

// Build compiles the spicedb-gen CLI.
func Build() error {
	return sh.RunV("go", "build", "./cmd/spicedb-gen")
}

// Test runs all Go unit tests.
func Test() error {
	return sh.RunV("go", "test", "-v", "./...")
}

// TypeScriptIntegrationTest builds the CLI, generates TypeScript code from the
// sample schema, type-checks it, starts SpiceDB, runs vitest, then stops SpiceDB.
func TypeScriptIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. Generate permissions.ts from sample.zed
	fmt.Println("==> Generating permissions.ts from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "typescript",
		"--out", "testdata/typescript/permissions.ts",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	defer os.Remove("testdata/typescript/permissions.ts")

	// 3. pnpm install in testdata/typescript/
	fmt.Println("==> Installing dependencies...")
	if err := runInDir("testdata/typescript", "pnpm", "install", "--frozen-lockfile=false"); err != nil {
		return fmt.Errorf("pnpm install failed: %w", err)
	}

	// 4. tsc --noEmit (type-checking including type_errors.ts)
	fmt.Println("==> Type-checking...")
	if err := runInDir("testdata/typescript", "pnpm", "run", "typecheck"); err != nil {
		return fmt.Errorf("type-check failed: %w", err)
	}

	// 5. Start SpiceDB via docker-compose
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

	// 6. pnpm run test (vitest)
	fmt.Println("==> Running vitest...")
	if err := runInDir("testdata/typescript", "pnpm", "run", "test"); err != nil {
		return fmt.Errorf("vitest failed: %w", err)
	}

	fmt.Println("==> TypeScript integration tests passed!")
	return nil
}

// GoIntegrationTest builds the CLI, generates Go code from the sample schema,
// verifies type safety, starts SpiceDB, runs go test, then stops SpiceDB.
func GoIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. Generate permissions.gen.go from sample.zed
	fmt.Println("==> Generating permissions.gen.go from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "go",
		"--out", "testdata/go/permissions.gen.go",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	defer os.Remove("testdata/go/permissions.gen.go")

	// 3. Verify generated code compiles
	fmt.Println("==> Verifying generated code compiles...")
	if err := runInDir("testdata/go", "go", "build", "."); err != nil {
		return fmt.Errorf("generated code does not compile: %w", err)
	}

	// 4. Verify type_errors_test.go does NOT compile (type safety check)
	fmt.Println("==> Verifying type errors are caught at compile time...")
	if err := verifyGoTypeErrors("testdata/go", "type_errors_test.go"); err != nil {
		return err
	}
	fmt.Println("    (every marked line fails to compile)")

	// 5. Start SpiceDB via docker-compose
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

	// 6. Run go test
	fmt.Println("==> Running Go integration tests...")
	if err := runInDir("testdata/go", "go", "test", "-v", "."); err != nil {
		return fmt.Errorf("go test failed: %w", err)
	}

	fmt.Println("==> Go integration tests passed!")
	return nil
}

// JavaIntegrationTest builds the CLI, generates Java code from the sample schema,
// verifies type safety, starts SpiceDB, runs Gradle tests, then stops SpiceDB.
func JavaIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. Generate Permissions.java from sample.zed
	fmt.Println("==> Generating Permissions.java from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "java",
		"--java.package=com.authzed.spicedb.gen.test",
		"--out", "testdata/java/src/main/java/com/authzed/spicedb/gen/test/Permissions.java",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	defer os.Remove("testdata/java/src/main/java/com/authzed/spicedb/gen/test/Permissions.java")

	// 3. Verify generated code and tests compile
	fmt.Println("==> Compiling generated code and tests...")
	if err := runInDir("testdata/java", "./gradlew", "compileTestJava"); err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}

	// 4. Verify type_errors do NOT compile (type safety check)
	fmt.Println("==> Verifying type errors are caught at compile time...")
	err := runInDir("testdata/java", "./gradlew", "compileTypeErrors")
	if err == nil {
		return fmt.Errorf("type errors compiled successfully but should have failed")
	}
	fmt.Println("    (expected compile failure confirmed)")

	// 5. Start SpiceDB via docker-compose
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

	// 6. Run Gradle tests
	fmt.Println("==> Running Java integration tests...")
	if err := runInDir("testdata/java", "./gradlew", "test"); err != nil {
		return fmt.Errorf("gradle test failed: %w", err)
	}

	fmt.Println("==> Java integration tests passed!")
	return nil
}

// pythonGenerated are the files the Python generator writes into the fixture
// package (testdata/python/testdata/, which already has an __init__.py):
// sync.py and aio.py import the types module with a package-relative import.
var pythonGenerated = []string{"permissions.py", "sync.py", "aio.py"}

// PythonIntegrationTest builds the CLI, generates Python code from the sample
// schema, checks ruff formatting and lint, type-checks it with pyright
// (positive, and per-line negative), then runs pytest against SpiceDB.
//
// When SPICEDB_ENDPOINT is set, pytest runs against that server and no
// container is started; otherwise docker-compose.test.yml starts one on
// localhost:50051.
func PythonIntegrationTest() error {
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	fmt.Println("==> Generating permissions.py, sync.py, and aio.py from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "python",
		"--out", "testdata/python/testdata",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	defer func() {
		for _, f := range pythonGenerated {
			os.Remove("testdata/python/testdata/" + f)
		}
	}()

	fmt.Println("==> Installing python deps via uv...")
	if err := runInDir("testdata/python", "uv", "sync", "--all-extras"); err != nil {
		return fmt.Errorf("uv sync failed: %w", err)
	}

	if err := PythonFormatCheck(); err != nil {
		return err
	}

	fmt.Println("==> Type-checking generated code with pyright...")
	if err := runInDir("testdata/python", "uv", "run", "pyright", "."); err != nil {
		return fmt.Errorf("pyright failed on project: %w", err)
	}

	fmt.Println("==> Verifying type errors are caught at type-check time...")
	if err := verifyPythonTypeErrors("testdata/python", "type_errors.py"); err != nil {
		return err
	}
	fmt.Println("    (every marked line fails to type-check)")

	endpoint := os.Getenv("SPICEDB_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:50051"
		fmt.Println("==> Starting SpiceDB...")
		if err := sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		defer func() {
			fmt.Println("==> Stopping SpiceDB...")
			_ = sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "down")
		}()
	}

	fmt.Printf("==> Waiting for SpiceDB at %s...\n", endpoint)
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	fmt.Println("==> Running pytest...")
	cmd := exec.Command("uv", "run", "pytest", "-v")
	cmd.Dir = "testdata/python"
	cmd.Env = append(os.Environ(), "SPICEDB_ENDPOINT="+endpoint)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pytest failed: %w", err)
	}

	fmt.Println("==> Python integration tests passed!")
	return nil
}

// PythonFormatCheck requires the generated Python fixture files to be
// `ruff format` clean and to pass the repository's ruff lint selection.
// Generated files are gitignored, so they are passed explicitly.
func PythonFormatCheck() error {
	fmt.Println("==> Checking generated Python with ruff format and ruff check...")
	args := append([]string{"run", "ruff", "format", "--isolated", "--check"}, pythonGeneratedPaths()...)
	if err := runInDir("testdata/python", "uv", args...); err != nil {
		return fmt.Errorf("generated Python is not ruff-format clean: %w", err)
	}
	args = append([]string{"run", "ruff", "check", "--isolated", "--select", "E4,E7,E9,F"}, pythonGeneratedPaths()...)
	if err := runInDir("testdata/python", "uv", args...); err != nil {
		return fmt.Errorf("generated Python fails ruff check: %w", err)
	}
	return nil
}

func pythonGeneratedPaths() []string {
	paths := make([]string, len(pythonGenerated))
	for i, f := range pythonGenerated {
		paths[i] = "testdata/" + f
	}
	return paths
}

// PythonTypeErrors verifies the Python negative type fixtures against
// already-generated code in testdata/python/testdata, without SpiceDB.
func PythonTypeErrors() error {
	return verifyPythonTypeErrors("testdata/python", "type_errors.py")
}

// verifyPythonTypeErrors runs pyright over file with JSON output and requires
// an error on each line marked "# should not type-check", and no error on any
// other line. A single failing line must not mask the others, which is what
// a bare "did pyright fail" check allows.
func verifyPythonTypeErrors(dir, file string) error {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	cmd := exec.Command("uv", "run", "pyright", "-p", "pyrightconfig.type_errors.json", "--outputjson")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		return fmt.Errorf("%s type-checked clean but should have failed", file)
	}
	var report struct {
		GeneralDiagnostics []struct {
			File     string `json:"file"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Range    struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"generalDiagnostics"`
	}
	if jerr := json.Unmarshal(out, &report); jerr != nil {
		return fmt.Errorf("pyright did not produce a JSON report (%v): %w\n%s", err, jerr, out)
	}
	failed := map[int]bool{}
	var unexpected []string
	marker := regexp.MustCompile(`#\s*should not type-check\s*$`)
	lines := strings.Split(string(src), "\n")
	for _, d := range report.GeneralDiagnostics {
		if d.Severity != "error" || filepath.Base(d.File) != file {
			continue
		}
		line := d.Range.Start.Line + 1 // pyright lines are 0-based
		failed[line] = true
		if line > len(lines) || !marker.MatchString(lines[line-1]) {
			unexpected = append(unexpected, fmt.Sprintf("%d: %s", line, d.Message))
		}
	}
	var missing []string
	for i, line := range lines {
		if marker.MatchString(line) && !failed[i+1] {
			missing = append(missing, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line)))
		}
	}
	if len(missing) > 0 || len(unexpected) > 0 {
		return fmt.Errorf("negative type fixture %s is wrong:\nmarked lines that type-checked:\n%s\nerrors on unmarked lines:\n%s",
			file, strings.Join(missing, "\n"), strings.Join(unexpected, "\n"))
	}
	return nil
}

// IntegrationTest runs TypeScript, Go, Java, and Python integration tests.
func IntegrationTest() error {
	if err := TypeScriptIntegrationTest(); err != nil {
		return err
	}
	if err := GoIntegrationTest(); err != nil {
		return err
	}
	if err := JavaIntegrationTest(); err != nil {
		return err
	}
	return PythonIntegrationTest()
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

func runInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// GoTypeErrors verifies the Go negative type fixtures against the checked-in
// generated code, without starting SpiceDB.
func GoTypeErrors() error {
	return verifyGoTypeErrors("testdata/go", "type_errors_test.go")
}

// verifyGoTypeErrors compiles dir's tests with the typeerror tag, reporting
// every error (-gcflags=-e), and requires a compile error on each line of file
// marked "// should not compile". A single failing line must not mask the
// others, which is what a bare "did it fail to build" check allows.
func verifyGoTypeErrors(dir, file string) error {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "test", "-c", "-o", os.DevNull, "-tags", "typeerror", "-gcflags=-e", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return fmt.Errorf("%s compiled successfully but should have failed", file)
	}
	failed := map[int]bool{}
	for _, m := range regexp.MustCompile(regexp.QuoteMeta(file)+`:(\d+):`).FindAllStringSubmatch(string(out), -1) {
		n, _ := strconv.Atoi(m[1])
		failed[n] = true
	}
	var missing []string
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "// should not compile") && !failed[i+1] {
			missing = append(missing, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line)))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("these lines compiled but should not have:\n%s\ncompiler output:\n%s", strings.Join(missing, "\n"), out)
	}
	return nil
}
