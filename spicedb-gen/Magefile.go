//go:build mage

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
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
	err := runInDir("testdata/go", "go", "test", "-c", "-o", os.DevNull, "-tags", "typeerror")
	if err == nil {
		return fmt.Errorf("type_errors_test.go compiled successfully but should have failed")
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

// PythonIntegrationTest builds the CLI, generates Python code from the sample schema,
// verifies type safety with pyright (positive + expected-failure), starts SpiceDB,
// runs pytest, then stops SpiceDB.
func PythonIntegrationTest() error {
	// 1. Build CLI
	fmt.Println("==> Building spicedb-gen...")
	if err := Build(); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	// 2. Generate permissions.py from sample.zed
	fmt.Println("==> Generating permissions.py from sample.zed...")
	if err := sh.RunV(
		"./spicedb-gen",
		"--schema", "testdata/sample.zed",
		"--lang", "python",
		"--out", "testdata/python/permissions.py",
	); err != nil {
		return fmt.Errorf("code generation failed: %w", err)
	}
	defer os.Remove("testdata/python/permissions.py")

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
