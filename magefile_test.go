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

// --- Gen.Summary ---

func TestResolveSummaryPathDefaultsWhenEmpty(t *testing.T) {
	if got := resolveSummaryPath(""); got != defaultSummaryPath {
		t.Fatalf("resolveSummaryPath(%q) = %q, want %q", "", got, defaultSummaryPath)
	}
}

func TestResolveSummaryPathDefaultsWhenWhitespace(t *testing.T) {
	if got := resolveSummaryPath("   "); got != defaultSummaryPath {
		t.Fatalf("resolveSummaryPath(%q) = %q, want %q", "   ", got, defaultSummaryPath)
	}
}

func TestResolveSummaryPathKeepsExplicitPath(t *testing.T) {
	if got := resolveSummaryPath("/tmp/pr-summary.md"); got != "/tmp/pr-summary.md" {
		t.Fatalf("resolveSummaryPath(explicit) = %q, want unchanged", got)
	}
}

// The prompt is the one piece of "which reviewer-facing rules did we actually
// ask for" logic in this target, and it is pure -- no git, no claude -- so it
// is tested directly rather than through an end-to-end run.
func TestSummaryPromptIncludesLogAndStat(t *testing.T) {
	prompt := summaryPrompt("abc123 gen: regenerate go proto client", "spicedb-go/client.go | 12 +++---", "/tmp/out.md")

	for _, want := range []string{
		"abc123 gen: regenerate go proto client",
		"spicedb-go/client.go | 12 +++---",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSummaryPromptTrimsLogAndStat(t *testing.T) {
	prompt := summaryPrompt("\n\n  abc123 commit  \n\n", "\n  stat line  \n", "/tmp/out.md")

	if strings.Contains(prompt, "\n\n\n") {
		t.Fatalf("prompt should not carry the untrimmed blank runs from log/stat:\n%s", prompt)
	}
	if !strings.Contains(prompt, "abc123 commit") {
		t.Fatalf("prompt missing trimmed log content:\n%s", prompt)
	}
	if !strings.Contains(prompt, "stat line") {
		t.Fatalf("prompt missing trimmed stat content:\n%s", prompt)
	}
}

func TestSummaryPromptNamesTheOutputFile(t *testing.T) {
	prompt := summaryPrompt("log", "stat", "/tmp/pr-summary-42.md")
	if !strings.Contains(prompt, "/tmp/pr-summary-42.md") {
		t.Fatalf("prompt does not tell Claude where to write the file:\n%s", prompt)
	}
}

// These pin the design's actual requirements for the reviewer-facing content,
// not just "the function returns a non-empty string" -- a prompt that dropped
// one of these instructions would still compile and still contain the log
// and stat, so it needs its own assertion.
func TestSummaryPromptCarriesRequiredInstructions(t *testing.T) {
	prompt := summaryPrompt("log", "stat", "out.md")

	for _, want := range []string{
		"cross-cutting",
		"public API change",
		"200-400 words",
		"ONLY the markdown body",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing required instruction %q:\n%s", want, prompt)
		}
	}
}

func TestCheckSummaryWrittenErrorsWhenFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.md")

	err := checkSummaryWritten(path)
	if err == nil {
		t.Fatal("expected an error when the summary file was never written, got nil")
	}
	if !strings.Contains(err.Error(), "did not write") {
		t.Fatalf("error should distinguish a missing file from an empty one, got: %v", err)
	}
}

func TestCheckSummaryWrittenErrorsWhenFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	err := checkSummaryWritten(path)
	if err == nil {
		t.Fatal("expected an error for an empty summary file, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error should distinguish an empty file from a missing one, got: %v", err)
	}
}

func TestCheckSummaryWrittenErrorsWhenFileWhitespaceOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitespace.md")
	if err := os.WriteFile(path, []byte("\n  \n"), 0o644); err != nil {
		t.Fatalf("write whitespace-only file: %v", err)
	}

	if err := checkSummaryWritten(path); err == nil {
		t.Fatal("expected an error for a whitespace-only summary file, got nil")
	}
}

func TestCheckSummaryWrittenPassesWhenFileHasContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(path, []byte("## Summary\n\nSomething changed.\n"), 0o644); err != nil {
		t.Fatalf("write summary file: %v", err)
	}

	if err := checkSummaryWritten(path); err != nil {
		t.Fatalf("unexpected error for a non-empty summary file: %v", err)
	}
}
