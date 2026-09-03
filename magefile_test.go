//go:build mage

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// --- runPool ---
//
// These exercise the pool directly, with a synthetic fn -- never mage or
// claude -- per this task's constraint against shelling out to either in
// tests. genProtoLangs/genClientLangs are thin wiring over this pool plus
// bufPinEnv/runMageIn*, which is exactly why the pool itself, and the
// canonical-order aggregation both callers share (failedLangs), are the
// testable unit.

// TestPoolNeverExceedsConcurrency is the pool's core bound: however many
// items it is given, no more than `concurrency` of fn's calls may be
// in-flight at once. Each call records how many calls were active (including
// itself) the instant it started; the max observed across the whole run must
// never exceed the bound.
func TestPoolNeverExceedsConcurrency(t *testing.T) {
	const n = 20
	const concurrency = 4

	var active int32
	var maxActive int32

	results := runPool(n, concurrency, func(i int) error {
		cur := atomic.AddInt32(&active, 1)
		for {
			m := atomic.LoadInt32(&maxActive)
			if cur <= m {
				break
			}
			if atomic.CompareAndSwapInt32(&maxActive, m, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return nil
	})

	if len(results) != n {
		t.Fatalf("runPool returned %d results, want %d", len(results), n)
	}
	if got := atomic.LoadInt32(&maxActive); got > concurrency {
		t.Fatalf("max concurrent fn calls = %d, want <= %d", got, concurrency)
	}
	// The bound should actually be exercised by this input, or the assertion
	// above would pass vacuously (e.g. if runPool silently ran everything
	// sequentially).
	if got := atomic.LoadInt32(&maxActive); got < concurrency {
		t.Fatalf("max concurrent fn calls = %d, want exactly %d (the pool never used its full bound; "+
			"either it under-parallelizes or this test is not exercising it)", got, concurrency)
	}
}

// TestPoolClampsConcurrencyBelowOne covers concurrency <= 0: runPool must
// still make progress (treat it as 1), not deadlock or panic on a
// zero-capacity semaphore channel.
func TestPoolClampsConcurrencyBelowOne(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		results := runPool(5, c, func(i int) error { return nil })
		if len(results) != 5 {
			t.Fatalf("concurrency=%d: runPool returned %d results, want 5", c, len(results))
		}
	}
}

// TestPoolClampsConcurrencyAboveN covers concurrency > n: runPool must not
// panic (e.g. on an oversized but otherwise harmless semaphore) and must
// still run every item exactly once.
func TestPoolClampsConcurrencyAboveN(t *testing.T) {
	const n = 3
	var calls int32
	results := runPool(n, 100, func(i int) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if len(results) != n {
		t.Fatalf("runPool returned %d results, want %d", len(results), n)
	}
	if got := atomic.LoadInt32(&calls); got != n {
		t.Fatalf("fn was called %d times, want %d", got, n)
	}
}

// TestPoolResultsIndexedByInput proves results[i] always corresponds to
// fn(i)'s own return value, regardless of the order calls actually complete
// in. Sleep durations are assigned in reverse of index order, so the first
// item to finish is the last index and vice versa -- if runPool assigned
// results by completion order instead of by index, this would catch it.
func TestPoolResultsIndexedByInput(t *testing.T) {
	const n = 6
	sentinel := func(i int) error { return fmt.Errorf("err-%d", i) }

	results := runPool(n, 3, func(i int) error {
		time.Sleep(time.Duration(n-i) * 5 * time.Millisecond)
		if i%2 == 0 {
			return sentinel(i)
		}
		return nil
	})

	for i := 0; i < n; i++ {
		if i%2 == 0 {
			want := sentinel(i).Error()
			if results[i] == nil || results[i].Error() != want {
				t.Fatalf("results[%d] = %v, want %q", i, results[i], want)
			}
		} else if results[i] != nil {
			t.Fatalf("results[%d] = %v, want nil", i, results[i])
		}
	}
}

// --- failedLangs ---
//
// This is the canonical-ordering and aggregate-failure-text guarantee both
// genProtoLangs and genClientLangs depend on: results are collected into a
// slice indexed by language, so a regeneration reports failures in langs'
// declared order regardless of which worker actually finished first -- and
// the format ("proto generation failed for: go, ruby") is exactly what the
// regen-from-api workflow greps for, so it must not drift.

func TestFailedLangsPreservesCanonicalOrderRegardlessOfResultOrder(t *testing.T) {
	langs := []string{"go", "python", "typescript", "csharp", "java", "ruby", "rust"}

	// Simulate completion order csharp, then go, then ruby (indices 3, 0, 5)
	// by populating results in that order -- but results is indexed by
	// position in langs, not by arrival, exactly as runPool guarantees.
	results := make([]error, len(langs))
	results[3] = errors.New("csharp failed")
	results[0] = errors.New("go failed")
	results[5] = errors.New("ruby failed")

	got := failedLangs(langs, results)
	want := []string{"go", "csharp", "ruby"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("failedLangs() = %v, want %v (langs' declared order, not completion order)", got, want)
	}
}

func TestFailedLangsAggregateMessageMatchesExistingFormat(t *testing.T) {
	langs := []string{"go", "python", "typescript", "csharp", "java", "ruby", "rust"}
	results := make([]error, len(langs))
	results[0] = errors.New("boom")
	results[5] = errors.New("boom")

	got := fmt.Errorf("proto generation failed for: %s", strings.Join(failedLangs(langs, results), ", "))
	want := "proto generation failed for: go, ruby"
	if got.Error() != want {
		t.Fatalf("aggregate error = %q, want %q -- the workflow greps for this exact string", got.Error(), want)
	}

	gotClient := fmt.Errorf("idiomatic client update failed for: %s", strings.Join(failedLangs(langs, []error{nil, nil, nil, nil, errors.New("x"), nil, nil}), ", "))
	wantClient := "idiomatic client update failed for: java"
	if gotClient.Error() != wantClient {
		t.Fatalf("aggregate error = %q, want %q", gotClient.Error(), wantClient)
	}
}

func TestFailedLangsEmptyWhenNoFailures(t *testing.T) {
	langs := []string{"go", "python"}
	if got := failedLangs(langs, make([]error, len(langs))); len(got) != 0 {
		t.Fatalf("failedLangs() = %v, want empty", got)
	}
}

// --- genConcurrency ---

// The design doc is explicit that the default is 4 -- pin the literal, not
// defaultGenConcurrency itself, or this test would pass no matter what that
// constant were changed to (it would just be comparing the constant to
// itself). See also TestDefaultGenConcurrencyIsFour.
func TestGenConcurrencyDefaultsWhenUnset(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "")
	if got := genConcurrency(); got != 4 {
		t.Fatalf("genConcurrency() = %d, want default 4", got)
	}
}

// TestDefaultGenConcurrencyIsFour pins the constant itself to the design
// doc's explicit default, independent of genConcurrency's own logic.
func TestDefaultGenConcurrencyIsFour(t *testing.T) {
	if defaultGenConcurrency != 4 {
		t.Fatalf("defaultGenConcurrency = %d, want 4 per the design doc", defaultGenConcurrency)
	}
}

func TestGenConcurrencyParsesOne(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "1")
	if got := genConcurrency(); got != 1 {
		t.Fatalf("genConcurrency() = %d, want 1", got)
	}
}

func TestGenConcurrencyParsesFour(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "4")
	if got := genConcurrency(); got != 4 {
		t.Fatalf("genConcurrency() = %d, want 4", got)
	}
}

func TestGenConcurrencyParsesArbitraryPositiveValue(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "7")
	if got := genConcurrency(); got != 7 {
		t.Fatalf("genConcurrency() = %d, want 7", got)
	}
}

func TestGenConcurrencyDefaultsOnGarbage(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "banana")
	if got := genConcurrency(); got != 4 {
		t.Fatalf("genConcurrency() = %d, want default 4 for an unparseable value", got)
	}
}

func TestGenConcurrencyClampsZeroToOne(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "0")
	if got := genConcurrency(); got != 1 {
		t.Fatalf("genConcurrency() = %d, want 1 (clamped)", got)
	}
}

func TestGenConcurrencyClampsNegativeToOne(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "-3")
	if got := genConcurrency(); got != 1 {
		t.Fatalf("genConcurrency() = %d, want 1 (clamped)", got)
	}
}

func TestGenConcurrencyTrimsWhitespace(t *testing.T) {
	t.Setenv("GEN_CONCURRENCY", "  2  ")
	if got := genConcurrency(); got != 2 {
		t.Fatalf("genConcurrency() = %d, want 2", got)
	}
}
