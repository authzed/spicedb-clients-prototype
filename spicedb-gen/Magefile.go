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
// sample schema, type-checks it (including the negative fixtures), starts
// SpiceDB, runs vitest, then stops SpiceDB. With SPICEDB_ENDPOINT set it uses
// that server instead of starting one.
func TypeScriptIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. pnpm install in testdata/typescript/ (the fixture project provides
	// the Prettier that formats the generated output).
	fmt.Println("==> Installing dependencies...")
	if err := runInDir("testdata/typescript", "pnpm", "install", "--frozen-lockfile=false"); err != nil {
		return fmt.Errorf("pnpm install failed: %w", err)
	}

	// 3. Generate permissions.ts from sample.zed inside the fixture project,
	// formatted with its Prettier, and check the formatting.
	fmt.Println("==> Generating permissions.ts from sample.zed...")
	if err := runInDir("testdata/typescript", "../../spicedb-gen",
		"--schema", "../sample.zed",
		"--lang", "typescript",
		"--typescript.format=prettier",
		"--out", "permissions.ts",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	if err := runInDir("testdata/typescript", "pnpm", "exec", "prettier", "--check", "permissions.ts"); err != nil {
		return fmt.Errorf("generated TypeScript is not Prettier-formatted: %w", err)
	}
	// A schema whose definition names collide with generated identifiers
	// (definition subject) and whose caveat calls a custom CEL function,
	// generated with --typescript.rename; the type-check below compiles it and
	// rename.test.ts exercises it.
	if err := runInDir("testdata/typescript", "../../spicedb-gen",
		"--schema", "rename.zed",
		"--lang", "typescript",
		"--typescript.format=prettier",
		"--typescript.rename=subject=SubjectDef",
		"--out", "renamed.ts",
	); err != nil {
		return fmt.Errorf("code generation with --typescript.rename failed: %w", err)
	}

	// 4. tsc --noEmit (type-checking including type_errors.ts). Every
	// forbidden line in type_errors.ts carries its own @ts-expect-error, and
	// tsc reports an unused directive as an error, so each line must fail on
	// its own; verifyTypeScriptTypeErrors keeps each directive on one line.
	fmt.Println("==> Type-checking...")
	if err := verifyTypeScriptTypeErrors("testdata/typescript/type_errors.ts"); err != nil {
		return err
	}
	if err := runInDir("testdata/typescript", "pnpm", "run", "typecheck"); err != nil {
		return fmt.Errorf("type-check failed: %w", err)
	}

	endpoint := os.Getenv("SPICEDB_ENDPOINT")
	if endpoint == "" {
		// 5. Start SpiceDB via docker-compose
		fmt.Println("==> Starting SpiceDB...")
		if err := sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		defer func() {
			fmt.Println("==> Stopping SpiceDB...")
			_ = sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "down")
		}()
		endpoint = "localhost:50051"
	}

	// Wait for SpiceDB to be ready
	fmt.Println("==> Waiting for SpiceDB to be ready...")
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	// 6. pnpm run test (vitest)
	fmt.Println("==> Running vitest...")
	cmd := exec.Command("pnpm", "run", "test")
	cmd.Dir = "testdata/typescript"
	cmd.Env = append(os.Environ(), "SPICEDB_ENDPOINT="+endpoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vitest failed: %w", err)
	}

	fmt.Println("==> TypeScript integration tests passed!")
	return nil
}

// TypeScriptTypeErrors verifies the TypeScript negative type fixtures against
// the checked-in generated code, without starting SpiceDB.
func TypeScriptTypeErrors() error {
	if err := verifyTypeScriptTypeErrors("testdata/typescript/type_errors.ts"); err != nil {
		return err
	}
	return runInDir("testdata/typescript", "pnpm", "run", "typecheck")
}

// verifyTypeScriptTypeErrors checks that every @ts-expect-error in file
// governs exactly one single-line statement. tsc then proves each of those
// lines fails on its own: an expect-error directive with no error on its
// line is itself a compile error (TS2578). A statement spanning several lines
// could hide a passing line behind a failing one, so it is rejected here.
func verifyTypeScriptTypeErrors(file string) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	lines := strings.Split(string(src), "\n")
	count := 0
	for i, line := range lines {
		if !strings.Contains(line, "@ts-expect-error") {
			continue
		}
		count++
		if i+1 >= len(lines) {
			return fmt.Errorf("%s:%d: @ts-expect-error at end of file", file, i+1)
		}
		next := strings.TrimSpace(lines[i+1])
		if next == "" || strings.HasPrefix(next, "//") || !strings.HasSuffix(next, ";") {
			return fmt.Errorf("%s:%d: @ts-expect-error must precede a single-line statement ending in ';', got %q", file, i+2, next)
		}
	}
	if count == 0 {
		return fmt.Errorf("%s has no @ts-expect-error lines", file)
	}
	fmt.Printf("==> %s: %d forbidden lines, each with its own @ts-expect-error\n", file, count)
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
// verifies formatting and type safety, runs Gradle tests against SpiceDB.
//
// SPICEDB_ENDPOINT (host:port) points the live tests at an existing SpiceDB and
// skips docker compose; unset, the compose service on localhost:50051 is used.
func JavaIntegrationTest() error {
	if err := generateJava(); err != nil {
		return err
	}
	defer os.Remove(javaGenerated)

	// 3. Verify generated code and tests compile, and that the generated file is
	// already google-java-format clean (spotlessCheck never rewrites it).
	fmt.Println("==> Compiling generated code and tests, checking google-java-format...")
	if err := runInDir("testdata/java", "./gradlew", "compileTestJava", "spotlessCheck"); err != nil {
		return fmt.Errorf("compilation or formatting check failed: %w", err)
	}

	// 4. Verify each negative type fixture line fails on its own.
	fmt.Println("==> Verifying type errors are caught at compile time...")
	if err := verifyJavaTypeErrors("testdata/java", "type_errors/com/authzed/spicedb/gen/test/TypeErrors.java"); err != nil {
		return err
	}
	fmt.Println("    (every marked line failed to compile)")

	endpoint := os.Getenv("SPICEDB_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:50051"
		// 5. Start SpiceDB via docker-compose
		fmt.Println("==> Starting SpiceDB...")
		if err := sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		defer func() {
			fmt.Println("==> Stopping SpiceDB...")
			_ = sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "down")
		}()
	}

	fmt.Println("==> Waiting for SpiceDB to be ready at " + endpoint + "...")
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	// 6. Run Gradle tests (they read SPICEDB_ENDPOINT).
	fmt.Println("==> Running Java integration tests...")
	cmd := exec.Command("./gradlew", "test")
	cmd.Dir = "testdata/java"
	cmd.Env = append(os.Environ(), "SPICEDB_ENDPOINT="+endpoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gradle test failed: %w", err)
	}

	fmt.Println("==> Java integration tests passed!")
	return nil
}

const javaGenerated = "testdata/java/src/main/java/com/authzed/spicedb/gen/test/Permissions.java"

// generateJava builds the CLI and generates Permissions.java from sample.zed.
func generateJava() error {
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	fmt.Println("==> Generating Permissions.java from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "java",
		"--java.package=com.authzed.spicedb.gen.test",
		"--out", javaGenerated,
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	return nil
}

// JavaTypeErrors generates the Java fixture and verifies the negative type
// fixtures and formatting without starting SpiceDB.
func JavaTypeErrors() error {
	if err := generateJava(); err != nil {
		return err
	}
	defer os.Remove(javaGenerated)
	if err := runInDir("testdata/java", "./gradlew", "spotlessCheck"); err != nil {
		return fmt.Errorf("generated Java is not google-java-format clean: %w", err)
	}
	return verifyJavaTypeErrors("testdata/java", "type_errors/com/authzed/spicedb/gen/test/TypeErrors.java")
}

// verifyJavaTypeErrors compiles the Java negative fixtures with every error
// reported (-Xmaxerrs) and requires: an error on each line of file marked
// "// should not compile", no error on any unmarked line (the fixture itself
// must otherwise compile, so an error cannot be blamed on the wrong line), and
// no "cannot find symbol" on a marked line (that means the API was renamed or
// removed, not that the schema constraint rejected the call).
func verifyJavaTypeErrors(dir, file string) error {
	src, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return err
	}
	cmd := exec.Command("./gradlew", "compileTypeErrors", "--console=plain", "--rerun-tasks")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return fmt.Errorf("%s compiled successfully but should have failed", file)
	}
	errs := map[int][]string{}
	for _, m := range regexp.MustCompile(regexp.QuoteMeta(filepath.Base(file))+`:(\d+): error: (.*)`).FindAllStringSubmatch(string(out), -1) {
		n, _ := strconv.Atoi(m[1])
		errs[n] = append(errs[n], m[2])
	}
	var problems []string
	marked := map[int]bool{}
	for i, line := range strings.Split(string(src), "\n") {
		if !strings.Contains(line, "// should not compile") {
			continue
		}
		marked[i+1] = true
		switch {
		case len(errs[i+1]) == 0:
			problems = append(problems, fmt.Sprintf("%d compiled but should not have: %s", i+1, strings.TrimSpace(line)))
		case strings.Contains(strings.Join(errs[i+1], " "), "cannot find symbol"):
			problems = append(problems, fmt.Sprintf("%d fails only because a symbol is missing: %s", i+1, strings.TrimSpace(line)))
		}
	}
	for n, msgs := range errs {
		if !marked[n] {
			problems = append(problems, fmt.Sprintf("%d has an unexpected error: %s", n, msgs[0]))
		}
	}
	if len(marked) == 0 {
		return fmt.Errorf("%s has no lines marked \"// should not compile\"", file)
	}
	if len(problems) > 0 {
		return fmt.Errorf("java negative type fixtures:\n%s\ncompiler output:\n%s", strings.Join(problems, "\n"), out)
	}
	fmt.Printf("    %d marked lines each failed to compile\n", len(marked))
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
