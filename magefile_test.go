//go:build mage

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goTemplate = `version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
inputs:
  - module: buf.build/authzed/api
`

// multiInputTemplate mirrors spicedb-typescript-proto: a second input whose
// paths filter is required by root DESIGN.md's "RULE: Error mapping must not
// lose the server's detail". Pinning must not touch it.
const multiInputTemplate = `version: v2
plugins:
  - remote: buf.build/bufbuild/es:v2.11.0
    out: src/gen
inputs:
  - module: buf.build/authzed/api
  - module: buf.build/googleapis/googleapis
    paths:
      - google/rpc/error_details.proto
`

func templateDir(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write buf.gen.yaml: %v", err)
	}
	return dir
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestBufGenTemplatePinsAuthzedInput(t *testing.T) {
	out, err := bufGenTemplate(templateDir(t, goTemplate), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(out)

	got := readAll(t, out)
	if !strings.Contains(got, "  - module: buf.build/authzed/api:abc123\n") {
		t.Fatalf("authzed input not pinned with indentation preserved:\n%s", got)
	}
}

func TestBufGenTemplateLeavesOtherInputsUntouched(t *testing.T) {
	out, err := bufGenTemplate(templateDir(t, multiInputTemplate), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(out)

	got := readAll(t, out)
	if !strings.Contains(got, "  - module: buf.build/googleapis/googleapis\n") {
		t.Fatalf("googleapis input was modified:\n%s", got)
	}
	if !strings.Contains(got, "      - google/rpc/error_details.proto\n") {
		t.Fatalf("googleapis paths filter was lost:\n%s", got)
	}
	if strings.Contains(got, "googleapis/googleapis:abc123") {
		t.Fatalf("googleapis input was wrongly pinned:\n%s", got)
	}
}

func TestBufGenTemplatePreservesPluginBlock(t *testing.T) {
	out, err := bufGenTemplate(templateDir(t, multiInputTemplate), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(out)

	got := readAll(t, out)
	if !strings.Contains(got, "  - remote: buf.build/bufbuild/es:v2.11.0\n") {
		t.Fatalf("pinned plugin version was altered:\n%s", got)
	}
}

func TestBufGenTemplateErrorsWhenAuthzedInputAbsent(t *testing.T) {
	body := "version: v2\ninputs:\n  - module: buf.build/googleapis/googleapis\n"
	if _, err := bufGenTemplate(templateDir(t, body), "abc123"); err == nil {
		t.Fatal("expected an error when no authzed/api input is present, got nil")
	}
}

func TestBufGenTemplateErrorsWhenTemplateMissing(t *testing.T) {
	if _, err := bufGenTemplate(t.TempDir(), "abc123"); err == nil {
		t.Fatal("expected an error when buf.gen.yaml does not exist, got nil")
	}
}

func TestBufPinEnvNoRefIsInert(t *testing.T) {
	env, cleanup, err := bufPinEnv(templateDir(t, goTemplate), "go", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if len(env) != 0 {
		t.Fatalf("expected no environment when BUFTAG is unset, got %v", env)
	}
}

func TestBufPinEnvRustUsesRawRef(t *testing.T) {
	env, cleanup, err := bufPinEnv(t.TempDir(), "rust", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if env["BUFTAG"] != "abc123" {
		t.Fatalf("rust should receive BUFTAG verbatim, got %v", env)
	}
	if _, ok := env["BUF_TEMPLATE"]; ok {
		t.Fatalf("rust has no buf.gen.yaml and must not get BUF_TEMPLATE, got %v", env)
	}
}

func TestBufPinEnvTemplateLangWritesPinnedTemplate(t *testing.T) {
	env, cleanup, err := bufPinEnv(templateDir(t, goTemplate), "go", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := env["BUF_TEMPLATE"]
	if path == "" {
		t.Fatalf("expected BUF_TEMPLATE to be set, got %v", env)
	}
	if !strings.Contains(readAll(t, path), "buf.build/authzed/api:abc123") {
		t.Fatalf("template at %s was not pinned", path)
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup should have removed %s, stat err = %v", path, err)
	}
}

// mkClientDirs creates spicedb-<lang> directories inside a fresh temp dir and
// chdirs into it, so stampLastGeneration tests never write into the real tree.
func mkClientDirs(t *testing.T, langs ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, l := range langs {
		if err := os.Mkdir(filepath.Join(dir, "spicedb-"+l), 0o755); err != nil {
			t.Fatalf("mkdir spicedb-%s: %v", l, err)
		}
	}
	t.Chdir(dir)
	return dir
}

func TestStampLastGenerationWritesRefForEveryLanguage(t *testing.T) {
	langs := []string{"go", "python", "typescript", "csharp", "java", "ruby", "rust"}
	dir := mkClientDirs(t, langs...)

	if err := stampLastGeneration(langs, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range langs {
		path := filepath.Join(dir, "spicedb-"+l, lastGenerationFile)
		if got := strings.TrimSpace(readAll(t, path)); got != "deadbeef" {
			t.Fatalf("%s = %q, want %q", path, got, "deadbeef")
		}
	}
}

// The readers strings.TrimSpace the baseline, and every hand-written
// .last-generation in the tree ends in a newline. Match that byte for byte.
func TestStampLastGenerationWritesRefPlusTrailingNewline(t *testing.T) {
	dir := mkClientDirs(t, "go")

	if err := stampLastGeneration([]string{"go"}, "abc123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path := filepath.Join(dir, "spicedb-go", lastGenerationFile)
	if got := readAll(t, path); got != "abc123\n" {
		t.Fatalf("%s = %q, want %q", path, got, "abc123\n")
	}
}

// A missing client directory must surface as an error rather than a silent
// no-op: silently skipping the stamp is exactly the failure mode this helper
// exists to end.
func TestStampLastGenerationErrorsWhenClientDirMissing(t *testing.T) {
	mkClientDirs(t, "go")

	err := stampLastGeneration([]string{"go", "nonexistent"}, "abc123")
	if err == nil {
		t.Fatal("expected an error when a client directory is absent, got nil")
	}
	if !strings.Contains(err.Error(), "spicedb-nonexistent") {
		t.Fatalf("error should name the offending path, got %v", err)
	}
}

// TestClaudePermissionArgsLocalIsUnchanged pins the promise this whole design
// rests on: a developer running mage locally, with CI_REGENERATION unset,
// must see exactly the invocation they see today -- no permission flag added
// on top of runClaudeOutput's unconditional --print.
func TestClaudePermissionArgsLocalIsUnchanged(t *testing.T) {
	t.Setenv("CI_REGENERATION", "")

	args := claudePermissionArgs()
	if args != nil {
		t.Fatalf("claudePermissionArgs() = %v, want nil when CI_REGENERATION is unset", args)
	}
}

// TestClaudePermissionArgsCIRegenerationAddsPermissionFlag reproduces the fix
// for the defect observed on runs 33010304938 vs 33010790114: without
// --permission-mode bypassPermissions, headless Claude replies that it lacks
// write permission, edits nothing, and still exits 0.
func TestClaudePermissionArgsCIRegenerationAddsPermissionFlag(t *testing.T) {
	t.Setenv("CI_REGENERATION", "1")

	got := claudePermissionArgs()
	want := []string{"--permission-mode", "bypassPermissions"}
	if len(got) != len(want) {
		t.Fatalf("claudePermissionArgs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claudePermissionArgs() = %v, want %v", got, want)
		}
	}
}
