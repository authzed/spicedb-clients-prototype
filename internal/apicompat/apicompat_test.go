package apicompat

import (
	"fmt"
	"os"
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
func fakeAPIDiff(t *testing.T, stdout string, code int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\necho 'boom' >&2\nexit %d\n", stdout, code)
	path := filepath.Join(dir, "go-apidiff")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestRunExitZeroPasses(t *testing.T) {
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
			fakeAPIDiff(t, "", code)
			if _, err := Run("deadbeef", ".."); err == nil {
				t.Fatal("operational failure must not be suppressed")
			}
		})
	}
}
