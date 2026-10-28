//go:build mage

package main

import (
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

// PythonIntegrationTest builds the CLI, generates Python code from the sample schema,
// verifies type safety with pyright (positive + expected-failure), starts SpiceDB,
// runs pytest, then stops SpiceDB.
func PythonIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. Generate permissions.py, sync.py, and aio.py from sample.zed. These
	// land in the "testdata" package (testdata/python/testdata/, which already
	// has an __init__.py) rather than the project root, since sync.py/aio.py
	// import permissions.py via a package-relative "from .permissions import"
	// — that only resolves when the three files live inside an actual package.
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
		for _, f := range []string{"permissions.py", "sync.py", "aio.py"} {
			os.Remove("testdata/python/testdata/" + f)
		}
	}()

	// 3. uv sync to install deps
	fmt.Println("==> Installing python deps via uv...")
	if err := runInDir("testdata/python", "uv", "sync", "--all-extras"); err != nil {
		return fmt.Errorf("uv sync failed: %w", err)
	}

	// 4. pyright over the project (excludes type_errors.py via the default pyrightconfig.json)
	fmt.Println("==> Type-checking generated code with pyright...")
	if err := runInDir("testdata/python", "uv", "run", "pyright", "."); err != nil {
		return fmt.Errorf("pyright failed on project: %w", err)
	}

	// 5. pyright over type_errors.py — must FAIL (uses a dedicated config that
	// includes only that file, since the default config excludes it).
	fmt.Println("==> Verifying type errors are caught at type-check time...")
	if err := runInDir("testdata/python", "uv", "run", "pyright", "-p", "pyrightconfig.type_errors.json"); err == nil {
		return fmt.Errorf("type_errors.py type-checked clean but should have failed")
	}
	fmt.Println("    (expected pyright failure confirmed)")

	// 6. Start SpiceDB
	fmt.Println("==> Starting SpiceDB...")
	if err := sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "up", "-d"); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	defer func() {
		fmt.Println("==> Stopping SpiceDB...")
		_ = sh.RunV("docker", "compose", "-f", "docker-compose.test.yml", "down")
	}()

	fmt.Println("==> Waiting for SpiceDB to be ready...")
	if err := waitForReady("localhost:50051", 30*time.Second); err != nil {
		return err
	}

	// 7. pytest
	fmt.Println("==> Running pytest...")
	if err := runInDir("testdata/python", "uv", "run", "pytest", "-v"); err != nil {
		return fmt.Errorf("pytest failed: %w", err)
	}

	fmt.Println("==> Python integration tests passed!")
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
