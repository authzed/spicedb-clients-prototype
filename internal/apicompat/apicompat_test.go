package apicompat

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// report wraps finding lines in the shape go-apidiff actually prints: a blank
// line, a package header, an indented section header, then indented findings.
func report(lines ...string) string {
	var b strings.Builder
	b.WriteString("\ngithub.com/authzed/spicedb-clients/spicedb-go/client\n")
	b.WriteString("  Incompatible changes:\n")
	for _, l := range lines {
		fmt.Fprintf(&b, "  - %s\n", l)
	}
	return b.String()
}

// realLookupResources is the verbatim finding produced by regenerating
// spicedb-go against authzed/api, taken from PR #79. It is the case this
// package exists to allow, and it carries both hazards that defeat naive
// parsing: a fully-qualified import path, and a result type containing a
// bracketed comma.
const realLookupResources = "(*Client).LookupResources: changed from " +
	"func(context.Context, github.com/authzed/spicedb-clients/spicedb-go/consistency.Strategy, " +
	"string, string, string, string) iter.Seq2[LookupResource, error] to " +
	"func(context.Context, github.com/authzed/spicedb-clients/spicedb-go/consistency.Strategy, " +
	"string, string, string, string, ...LookupResourcesOption) iter.Seq2[LookupResource, error]"

func TestFilterAllows(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"real regeneration case from PR #79", realLookupResources},
		{"package-level func", "Foo: changed from func(int) int to func(int, ...Option) int"},
		{"pointer-receiver method", "(*Client).Check: changed from func(string) error to func(string, ...Option) error"},
		{"generic pointer receiver", "(*Generic[T]).Do: changed from func(int) error to func(int, ...Option) error"},
		{"no parameters before", "Zero: changed from func() int to func(...Option) int"},
		// apidiff strips top-level parameter names but not nested ones, so a
		// nested function type still renders with its own parameter names.
		{"nested func type keeps names", "Nested: changed from func(func(a int, b int)) int to func(func(a int, b int), ...Option) int"},
		{"multiple existing params", "Many: changed from func(string, int) error to func(string, int, ...Option) error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Filter(strings.NewReader(report(tc.line)))
			if err != nil {
				t.Fatalf("Filter: %v", err)
			}
			if !res.OK() {
				t.Fatalf("expected allowed, got blocked: %v", res.Blocked)
			}
			if len(res.Allowed) != 1 {
				t.Fatalf("Allowed = %d findings, want 1", len(res.Allowed))
			}
		})
	}
}

func TestFilterBlocks(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		// The three ambiguous forms. apidiff renders an interface method, a
		// func-typed struct field and a value-receiver method identically, so
		// none of them can be allowed: two of the three are hard breaks.
		{"interface method", "Doer.Do: changed from func(string) error to func(string, ...Option) error"},
		{"func-typed struct field", "Holder.Check: changed from func(int) error to func(int, ...Option) error"},
		{"value-receiver method", "Val.Check: changed from func(string) error to func(string, ...Option) error"},

		{"removal", "Baz: removed"},
		{"parameter type changed", "Qux: changed from func(string) string to func(int) string"},
		{"required parameter added", "Bar: changed from func(int) int to func(int, int) int"},
		// A required parameter smuggled in alongside the variadic one.
		{"required param added beside variadic", "Two: changed from func(int) int to func(int, string, ...Option) int"},
		{"variadic type swapped", "Swap: changed from func(int, ...A) int to func(int, ...B) int"},
		{"variadic removed", "Drop: changed from func(int, ...Option) int to func(int) int"},
		{"result changed", "Res: changed from func(int) int to func(int, ...Option) error"},
		{"no longer implements", "Client: no longer implements Checker"},
		{"unrecognised shape", "Weird: something entirely different"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Filter(strings.NewReader(report(tc.line)))
			if err != nil {
				t.Fatalf("Filter: %v", err)
			}
			if res.OK() {
				t.Fatalf("expected blocked, but gate passed; allowed=%v", res.Allowed)
			}
			if len(res.Blocked) != 1 {
				t.Fatalf("Blocked = %d findings, want 1", len(res.Blocked))
			}
		})
	}
}

// TestFilterMixed checks that one genuine break still fails the gate even when
// allowable changes are present -- the allowance must not mask its neighbour.
func TestFilterMixed(t *testing.T) {
	res, err := Filter(strings.NewReader(report(
		realLookupResources,
		"Baz: removed",
	)))
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if res.OK() {
		t.Fatal("gate passed despite a removal")
	}
	if len(res.Allowed) != 1 || len(res.Blocked) != 1 {
		t.Fatalf("Allowed=%d Blocked=%d, want 1 and 1", len(res.Allowed), len(res.Blocked))
	}
}

// TestFilterIgnoresCompatibleSection guards against a compatible change being
// mistaken for an incompatible one when --print-compatible is in use.
func TestFilterIgnoresCompatibleSection(t *testing.T) {
	in := "\npkg\n  Incompatible changes:\n  - Baz: removed\n  Compatible changes:\n  - New: added\n  - S.B: added\n"
	res, err := Filter(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(res.Blocked) != 1 || res.Blocked[0].Name != "Baz" {
		t.Fatalf("Blocked = %v, want only Baz", res.Blocked)
	}
	if len(res.Allowed) != 0 {
		t.Fatalf("Allowed = %v, want none", res.Allowed)
	}
}

func TestFilterEmptyPasses(t *testing.T) {
	res, err := Filter(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if !res.OK() {
		t.Fatalf("empty report should pass, got %v", res.Blocked)
	}
}

// fakeAPIDiff puts a stub go-apidiff on PATH that prints stdout and exits with
// code. It exercises Run's exit-status handling, which is the property that
// keeps a tool failure from being mistaken for a clean report.
//
// The stub also deletes ../UNTRACKED relative to its own working directory,
// mimicking the real tool: go-apidiff checks commits out in place and wipes
// every untracked and ignored file in the repository it is pointed at. That is
// what TestRunIsolatesTheWorkingTree measures.
func fakeAPIDiff(t *testing.T, stdout string, code int) {
	t.Helper()
	dir := t.TempDir()
	// Faithful on the two dimensions that matter: it resolves the base ref in
	// the repository it was pointed at, failing with go-apidiff's own exit 2
	// and message when it cannot, and it deletes untracked files there.
	script := fmt.Sprintf(`#!/bin/sh
base=""; repo="."
while [ $# -gt 0 ]; do
  case "$1" in
    --repo-path) repo="$2"; shift 2 ;;
    *) if [ -z "$base" ]; then base="$1"; fi; shift ;;
  esac
done
if ! git -C "$repo" rev-parse --verify "$base" >/dev/null 2>&1; then
  echo "failed to lookup git commit hashes: could not get hash for \"$base\": reference not found" >&2
  exit 2
fi
rm -rf ../UNTRACKED
cat <<'APIDIFFOUT'
%s
APIDIFFOUT
echo 'boom' >&2
exit %d
`, stdout, code)
	path := filepath.Join(dir, "go-apidiff")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// repoWithModule creates a throwaway git repository holding one module
// directory plus an untracked file, and chdirs into the module -- the shape
// Run is called in, where repoPath is ".." and the module is the caller's
// working directory. It returns the repository root.
func repoWithModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "mod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatalf("creating module dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mod, "tracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing tracked file: %v", err)
	}
	// Two commits, so that HEAD~1 -- the shape a real caller passes -- resolves.
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t.t"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-qm", "first"},
		{"commit", "-qm", "second", "--allow-empty"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// Untracked, and therefore exactly what the real tool destroys.
	if err := os.WriteFile(filepath.Join(root, "UNTRACKED"), []byte("keep me"), 0o644); err != nil {
		t.Fatalf("writing untracked file: %v", err)
	}
	t.Chdir(mod)
	return root
}

func TestRunExitZeroPasses(t *testing.T) {
	repoWithModule(t)
	fakeAPIDiff(t, "", 0)
	res, err := Run("HEAD~1", "..")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK() {
		t.Fatalf("exit 0 should pass, got %v", res.Blocked)
	}
}

func TestRunExitOneIsClassified(t *testing.T) {
	repoWithModule(t)
	fakeAPIDiff(t, report(realLookupResources), 1)
	res, err := Run("HEAD~1", "..")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK() {
		t.Fatalf("additive variadic should pass, got %v", res.Blocked)
	}
	if len(res.Allowed) != 1 {
		t.Fatalf("Allowed = %d, want 1", len(res.Allowed))
	}
}

func TestRunExitOneStillBlocksRealBreaks(t *testing.T) {
	repoWithModule(t)
	fakeAPIDiff(t, report("Baz: removed"), 1)
	res, err := Run("HEAD~1", "..")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.OK() {
		t.Fatal("a removal must not pass the gate")
	}
}

// TestRunOperationalFailure is the regression guard for the defect that makes
// piping go-apidiff into a filter unsafe: exit 2 prints no findings, so a
// filter sees an empty report and would otherwise pass the gate.
func TestRunOperationalFailure(t *testing.T) {
	for _, code := range []int{2, 3} {
		t.Run(fmt.Sprintf("exit %d", code), func(t *testing.T) {
			repoWithModule(t)
			fakeAPIDiff(t, "", code)
			if _, err := Run("deadbeef", ".."); err == nil {
				t.Fatal("operational failure must not be suppressed")
			}
		})
	}
}

// TestRunIsolatesTheWorkingTree is the regression guard for the defect that
// made the java and typescript gates fail during regeneration. go-apidiff
// checks commits out in place and wipes every untracked and ignored file in the
// repository -- tools/japicmp.jar, node_modules/, .venv/ -- and because Go runs
// first among the language gates, it removed what the later gates needed.
// Run must therefore point the tool at a scratch clone, never the caller's tree.
func TestRunIsolatesTheWorkingTree(t *testing.T) {
	root := repoWithModule(t)
	fakeAPIDiff(t, "", 0)

	if _, err := Run("HEAD", ".."); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "UNTRACKED")); err != nil {
		t.Fatalf("go-apidiff was run against the caller's working tree and destroyed an "+
			"untracked file; it must run against a scratch clone: %v", err)
	}
}

// git creates the shape a CI checkout has: a detached HEAD with no local
// branches, where the base is only reachable as a remote-tracking ref.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestRunResolvesARemoteTrackingBase is the regression guard for the first
// version of the scratch clone, which failed in CI with "could not get hash for
// origin/main: reference not found".
//
// git clone maps the source's local branches into the clone's origin/*, and
// copies only the objects those reach. actions/checkout leaves a detached HEAD
// with no local branches at all, so a plain clone of it resolves neither
// origin/main nor necessarily the objects behind it -- while the source repo
// resolves it perfectly well, which is what makes the failure invisible
// locally.
func TestRunResolvesARemoteTrackingBase(t *testing.T) {
	tmp := t.TempDir()
	upstream := filepath.Join(tmp, "upstream.git")
	work := filepath.Join(tmp, "work")
	ci := filepath.Join(tmp, "ci")

	if out, err := exec.Command("git", "init", "-q", "--bare", upstream).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "clone", "-q", upstream, work).CombinedOutput(); err != nil {
		t.Fatalf("clone work: %v: %s", err, out)
	}
	gitIn(t, work, "config", "user.email", "t@t.t")
	gitIn(t, work, "config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(work, "mod"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "mod", "tracked.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-qm", "one")
	gitIn(t, work, "push", "-q", "origin", "HEAD:main")

	if out, err := exec.Command("git", "clone", "-q", upstream, ci).CombinedOutput(); err != nil {
		t.Fatalf("clone ci: %v: %s", err, out)
	}
	// Exactly what actions/checkout leaves behind: detached, no local branches,
	// with the base reachable only as refs/remotes/origin/main.
	gitIn(t, ci, "checkout", "-q", "--detach", "origin/main")
	for _, b := range []string{"main", "master"} {
		// Whichever the local default was; absent is fine.
		_ = exec.Command("git", "-C", ci, "branch", "-q", "-D", b).Run()
	}
	if out, err := exec.Command("git", "-C", ci, "for-each-ref", "--format=%(refname)", "refs/heads").Output(); err != nil {
		t.Fatalf("listing local branches: %v", err)
	} else if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("test setup did not reproduce a branchless checkout: %s", out)
	}

	fakeAPIDiff(t, "", 0)
	t.Chdir(filepath.Join(ci, "mod"))

	if _, err := Run("origin/main", ".."); err != nil {
		t.Fatalf("a remote-tracking base must resolve inside the scratch clone: %v", err)
	}
}
