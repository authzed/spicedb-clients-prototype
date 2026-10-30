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
	if err := sh.RunV("./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "typescript",
		"--typescript.names-only",
		"--out", "testdata/typescript-names/names.gen.ts",
	); err != nil {
		return fmt.Errorf("code generation with --typescript.names-only failed: %w", err)
	}
	if err := verifyTypeScriptNamesOnly(); err != nil {
		return err
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
	if err := runInDir("testdata/typescript", "pnpm", "run", "typecheck"); err != nil {
		return err
	}
	return verifyTypeScriptNamesOnly()
}

// verifyTypeScriptNamesOnly type-checks the --typescript.names-only fixture
// (testdata/typescript-names) in a temporary directory with no node_modules
// above it, so an import of @spicedb/* (or any package) cannot resolve: the
// names-only output must type-check with no client dependency. It uses the
// fixture project's tsc.
func verifyTypeScriptNamesOnly() error {
	fmt.Println("==> Type-checking the names-only output with no packages installed...")
	tsc, err := filepath.Abs("testdata/typescript/node_modules/.bin/tsc")
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "spicedb-gen-names-only-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	entries, err := os.ReadDir("testdata/typescript-names")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join("testdata/typescript-names", e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, e.Name()), b, 0o644); err != nil {
			return err
		}
	}
	if err := runInDir(tmp, tsc, "-p", "."); err != nil {
		return fmt.Errorf("names-only TypeScript does not type-check without the client: %w", err)
	}
	return nil
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
	// permissions.gen.go is tracked (other targets and the fixture-is-current
	// test read it), so regeneration leaves it in place rather than deleting it.

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
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "go",
		"--go.package=apinames",
		"--go.names-only",
		"--out", "testdata/go-names/names.gen.go",
	); err != nil {
		return fmt.Errorf("code generation with --go.names-only failed: %w", err)
	}
	if err := verifyGoNamesOnly(); err != nil {
		return err
	}

	// 5. Start SpiceDB via docker-compose, unless SPICEDB_ENDPOINT names one.
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

	// Wait for SpiceDB to be ready
	fmt.Println("==> Waiting for SpiceDB to be ready at " + endpoint + "...")
	if err := waitForReady(endpoint, 30*time.Second); err != nil {
		return err
	}

	// 6. Run go test (the tests read SPICEDB_ENDPOINT).
	fmt.Println("==> Running Go integration tests...")
	goTest := exec.Command("go", "test", "-v", ".")
	goTest.Dir = "testdata/go"
	goTest.Env = append(os.Environ(), "SPICEDB_ENDPOINT="+endpoint)
	goTest.Stdout = os.Stdout
	goTest.Stderr = os.Stderr
	if err := goTest.Run(); err != nil {
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
	defer os.Remove(javaNamesOnlyGenerated)

	// 3. Verify generated code and tests compile, and that the generated file is
	// already google-java-format clean (spotlessCheck never rewrites it).
	fmt.Println("==> Compiling generated code and tests, checking google-java-format...")
	if err := runInDir("testdata/java", "./gradlew", "compileTestJava", "compileNamesOnly", "spotlessCheck"); err != nil {
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

// javaNamesOnlyGenerated is the --java.names-only output, which the
// compileNamesOnly task compiles with an empty classpath.
const javaNamesOnlyGenerated = "testdata/java/names_only/com/authzed/spicedb/gen/names/PermissionNames.java"

// generateJava builds the CLI and generates Permissions.java from sample.zed,
// plus the names-only PermissionNames.java.
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
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "java",
		"--java.package=com.authzed.spicedb.gen.names",
		"--java.names-only",
		"--out", javaNamesOnlyGenerated,
	); err != nil {
		return fmt.Errorf("code generation with --java.names-only failed: %w", err)
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
	defer os.Remove(javaNamesOnlyGenerated)
	if err := runInDir("testdata/java", "./gradlew", "spotlessCheck"); err != nil {
		return fmt.Errorf("generated Java is not google-java-format clean: %w", err)
	}
	// The names-only output compiles with an empty classpath: no spicedb-java.
	if err := runInDir("testdata/java", "./gradlew", "compileNamesOnly", "--rerun-tasks"); err != nil {
		return fmt.Errorf("names-only Java does not compile without spicedb-java: %w", err)
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
// schema, checks ruff formatting and lint, type-checks it with pyright and mypy
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

	if err := PythonMypy(); err != nil {
		return err
	}

	fmt.Println("==> Verifying type errors are caught at type-check time...")
	if err := verifyPythonTypeErrors("testdata/python", "type_errors.py"); err != nil {
		return err
	}
	fmt.Println("    (every marked line fails to type-check)")
	if err := verifyPythonNamesOnly(); err != nil {
		return err
	}

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

// PythonMypy type-checks the generated package with mypy, both with its
// defaults and with --strict, so applications that run mypy on their source
// tree (generated code included) see no errors from it. pyright runs
// separately; both checkers must stay clean.
func PythonMypy() error {
	fmt.Println("==> Type-checking generated code with mypy (default and --strict)...")
	if err := runInDir("testdata/python", "uv", "run", "mypy", "-p", "testdata"); err != nil {
		return fmt.Errorf("mypy failed on the generated package: %w", err)
	}
	if err := runInDir("testdata/python", "uv", "run", "mypy", "--strict", "-p", "testdata"); err != nil {
		return fmt.Errorf("mypy --strict failed on the generated package: %w", err)
	}
	return nil
}

// PythonFormatCheck requires the generated Python fixture files to be
// `ruff format` clean and to pass the repository's ruff lint selection plus
// the unused-argument rules (ARG).
// Generated files are gitignored, so they are passed explicitly.
func PythonFormatCheck() error {
	fmt.Println("==> Checking generated Python with ruff format and ruff check...")
	args := append([]string{"run", "ruff", "format", "--isolated", "--check"}, pythonGeneratedPaths()...)
	if err := runInDir("testdata/python", "uv", args...); err != nil {
		return fmt.Errorf("generated Python is not ruff-format clean: %w", err)
	}
	args = append([]string{"run", "ruff", "check", "--isolated", "--select", "E4,E7,E9,F,ARG"}, pythonGeneratedPaths()...)
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
	if err := verifyPythonTypeErrors("testdata/python", "type_errors.py"); err != nil {
		return err
	}
	return verifyPythonNamesOnly()
}

// verifyPythonNamesOnly runs the --python.names-only fixture
// (testdata/python-names) with `python3 -I -S`, so no site-packages are
// importable: it passes only while names.py imports the standard library
// alone. It then type-checks names.py with mypy --strict. A unit test keeps
// names.py current with the generator.
func verifyPythonNamesOnly() error {
	fmt.Println("==> Running the names-only output with no site-packages...")
	if err := sh.RunV("python3", "-I", "-S", "testdata/python-names/check_names.py"); err != nil {
		return fmt.Errorf("names-only Python output does not load without the client: %w", err)
	}
	if err := runInDir("testdata/python", "uv", "run", "mypy", "--strict", "../python-names/names.py"); err != nil {
		return fmt.Errorf("mypy --strict failed on the names-only output: %w", err)
	}
	return nil
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
	if err := verifyGoTypeErrors("testdata/go", "type_errors_test.go"); err != nil {
		return err
	}
	if err := verifyGoNamesOnly(); err != nil {
		return err
	}
	return verifyGoStrict()
}

// staticcheckVersion pins the staticcheck release used on generated Go.
const staticcheckVersion = "honnef.co/go/tools/cmd/staticcheck@v0.7.0"

// verifyGoStrict holds the generated Go (full and names-only) to the strictest
// standard checks: go vet, then staticcheck with every check enabled
// (including the style checks). Generated code must pass both unmodified.
func verifyGoStrict() error {
	fmt.Println("==> Vetting and linting the generated Go (go vet, staticcheck -checks all)...")
	for _, dir := range []string{"testdata/go", "testdata/go-names"} {
		env := []string{}
		if dir == "testdata/go-names" {
			env = []string{"GOWORK=off", "GOFLAGS=-mod=readonly"}
		}
		for _, args := range [][]string{
			{"vet", "./..."},
			{"run", staticcheckVersion, "-checks", "all", "./..."},
		} {
			cmd := exec.Command("go", args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("generated Go in %s fails go %s: %w", dir, args[0], err)
			}
		}
	}
	return nil
}

// verifyGoNamesOnly builds and tests the --go.names-only fixture
// (testdata/go-names), a module whose go.mod requires nothing: it builds only
// while the names-only output imports the standard library alone.
func verifyGoNamesOnly() error {
	fmt.Println("==> Testing the names-only output in a module with no requirements...")
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = "testdata/go-names"
	// No -mod=mod: a non-standard import must fail, not be added to go.mod.
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("names-only Go output does not build without the client: %w", err)
	}
	return nil
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
