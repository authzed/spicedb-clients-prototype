// Package apicompat re-classifies go-apidiff findings so that adding a
// trailing variadic option parameter to an existing exported function or
// concrete method does not fail the Go API-compatibility gate.
//
// Regeneration routinely widens a client call by appending an option
// parameter -- `LookupResources(ctx, ...)` becoming
// `LookupResources(ctx, ..., opts ...LookupResourcesOption)`. Every existing
// call site still compiles, but go-apidiff reports it as incompatible and
// exits 1, because x/exp/apidiff compares signatures for type identity and the
// Go spec makes a variadic function type non-identical to its non-variadic
// counterpart. Upstream does this deliberately (apidiff's README calls out
// `var v func(int) = pkg.F` as the reason, and testdata/functions.go pins it),
// so the gate has to be loosened here rather than fixed there.
//
// There is no flag for this. go-apidiff has exactly three flags and no
// allowlist; two PRs adding package-level ignores have sat unmerged for years
// and the maintainer declined the concept. Nor is there a library route:
// apidiff.Change carries only a preformatted Message and a Compatible bool --
// no types, no category -- and go-apidiff's own Diff keeps its reports
// unexported behind IsCompatible() and a PrintReports that writes to os.Stdout.
// Parsing the printed messages is the only available seam, which the go-apidiff
// maintainer confirms in issue #21.
//
// # Why string scanning rather than go/parser
//
// The obvious refinement -- parse both halves with go/parser.ParseExpr and
// compare *ast.FuncType structurally -- does not survive real input. apidiff
// renders types with go/types.TypeString, which emits fully-qualified import
// paths, and `func(github.com/authzed/spicedb-clients/spicedb-go/consistency.Strategy)`
// is not valid Go syntax: ParseExpr rejects it with "missing ',' in parameter
// list". Making it parse would require rewriting qualified paths into synthetic
// identifiers first, and that rewrite is a regex that can itself misfire. Since
// both halves come from the same canonical renderer, byte equality of the
// parameter text already *is* structural equality, so this package scans with
// bracket-depth awareness instead. That handles the two shapes real output
// contains: nested function types keep their parameter names even though
// top-level ones are stripped (`func(func(a int, b int))`), and result types
// carry bracketed commas (`iter.Seq2[LookupResource, error]`).
//
// # Why only two name forms are eligible
//
// apidiff renders three very different severities identically. Given
// `X.Y: changed from func(int) error to func(int, ...Option) error`, X may be
// an interface (the change breaks every implementor), a struct with a
// func-typed field (it breaks every composite literal), or a concrete type with
// a value receiver (benign). Nothing in the message distinguishes them. Only
// two forms are self-identifying: a bare `Foo` is a package-level function, and
// `(*T).M` must be a concrete method because interfaces cannot have pointer
// receivers. Those two are eligible; the ambiguous `X.Y` form is always
// blocked.
//
// # What this deliberately cannot catch
//
// Appending a variadic still breaks any consumer that writes the function type
// down -- assignment to an explicit func type, a func-typed struct field, or an
// interface declared in the consumer's own module that the client used to
// satisfy. That last case is invisible to any tool run inside this repo. It is
// also worth knowing that apidiff will not catch the in-repo version of it for
// the form this package allows: golang/go#55963, open since 2022, means
// apidiff tests interface satisfaction against the value type only, so a
// pointer-receiver method change never produces a "no longer implements" line.
// Allowed findings are therefore returned, not discarded, so callers can
// announce them in the PR body and changelog rather than suppressing them
// silently. If spicedb-go ever declares an interface meant to be satisfied by
// *Client, add `var _ Iface = (*Client)(nil)` so the compiler catches what
// apidiff structurally cannot.
package apicompat

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// operationalExitCode is the exit status at or above which go-apidiff is
// reporting a failure of its own (bad ref, dirty worktree, package load error)
// rather than an API finding. It exits 1 for incompatible changes and 2 for
// everything else.
const operationalExitCode = 2

var (
	// changeRe matches a rendered signature change. The name is non-greedy so
	// it stops at the first ": changed from", and both halves are anchored on
	// "func(" so a non-signature change (for example "X: removed") never
	// matches and is therefore blocked.
	changeRe = regexp.MustCompile(`^- (.+?): changed from (func\(.*) to (func\(.*)$`)
	// nameRe extracts the changed object's name from any finding, including
	// ones that are not signature changes, so that blocked findings can be
	// reported by name too. Rendered names never contain a colon.
	nameRe = regexp.MustCompile(`^- (.+?): `)
	// bareRe matches a package-level function: no receiver, so no interface.
	bareRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// ptrRe matches a pointer-receiver method, optionally generic. Interfaces
	// cannot have pointer receivers, so this form is necessarily concrete.
	ptrRe = regexp.MustCompile(`^\(\*[A-Za-z_][A-Za-z0-9_]*(\[[^\]]*\])?\)\.[A-Za-z_][A-Za-z0-9_]*$`)
)

// A Finding is one incompatible change reported by go-apidiff.
type Finding struct {
	// Name is the changed object, such as "(*Client).LookupResources".
	Name string
	// Line is the reported text, without go-apidiff's leading indentation.
	Line string
}

// A Result is a go-apidiff report partitioned by this package's policy.
type Result struct {
	// Allowed holds additive-variadic changes on eligible name forms. They are
	// still breaking for assignment and for consumer-declared interfaces, so
	// callers should report them rather than drop them.
	Allowed []Finding
	// Blocked holds every other incompatible change.
	Blocked []Finding
}

// OK reports whether the gate should pass.
func (r Result) OK() bool { return len(r.Blocked) == 0 }

// Filter partitions go-apidiff's output. Only the "Incompatible changes:"
// section is considered; compatible changes are already non-breaking.
func Filter(r io.Reader) (Result, error) {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	inIncompatible := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Incompatible changes:"):
			inIncompatible = true
			continue
		case strings.HasPrefix(line, "Compatible changes:"):
			inIncompatible = false
			continue
		}
		if !inIncompatible || !strings.HasPrefix(line, "- ") {
			continue
		}

		f := Finding{Line: line}
		if n := nameRe.FindStringSubmatch(line); n != nil {
			f.Name = n[1]
		}
		m := changeRe.FindStringSubmatch(line)
		if m == nil {
			// Not a signature change at all -- a removal, or a shape this
			// package does not recognise. Block it.
			res.Blocked = append(res.Blocked, f)
			continue
		}
		if eligibleName(f.Name) && isAdditiveVariadic(m[2], m[3]) {
			res.Allowed = append(res.Allowed, f)
			continue
		}
		res.Blocked = append(res.Blocked, f)
	}
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("reading go-apidiff output: %w", err)
	}
	return res, nil
}

// eligibleName reports whether a name form is one whose receiver kind is
// unambiguous. See this package's doc comment.
func eligibleName(name string) bool {
	return bareRe.MatchString(name) || ptrRe.MatchString(name)
}

// isAdditiveVariadic reports whether newSig is oldSig with exactly one extra
// trailing variadic parameter and identical results.
func isAdditiveVariadic(oldSig, newSig string) bool {
	oldParams, oldResults, ok := splitSignature(oldSig)
	if !ok {
		return false
	}
	newParams, newResults, ok := splitSignature(newSig)
	if !ok || oldResults != newResults {
		return false
	}

	head, last := splitLastParam(newParams)
	// apidiff strips top-level parameter names, but tolerate one defensively
	// in case that rendering ever changes.
	if i := strings.Index(last, " ..."); i >= 0 && !strings.HasPrefix(last, "...") {
		last = strings.TrimSpace(last[i:])
	}
	if !strings.HasPrefix(last, "...") {
		return false
	}
	// Everything before the appended parameter must be untouched. This is what
	// rejects `func(int)` -> `func(int, string, ...Option)`, which smuggles a
	// required parameter in alongside the variadic one.
	return head == strings.TrimSpace(oldParams)
}

// splitSignature returns the top-level parameter text and the result text of a
// rendered "func(...) results" signature, tracking bracket depth so that nested
// function types and bracketed generic arguments do not terminate the scan.
func splitSignature(sig string) (params, results string, ok bool) {
	const prefix = "func("
	if !strings.HasPrefix(sig, prefix) {
		return "", "", false
	}
	depth := 0
	for i := len(prefix) - 1; i < len(sig); i++ {
		switch sig[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return sig[len(prefix):i], strings.TrimSpace(sig[i+1:]), true
			}
		}
	}
	return "", "", false
}

// splitLastParam splits a parameter list at its last top-level comma, scanning
// backwards so that a comma inside a nested type does not split it.
func splitLastParam(params string) (head, last string) {
	depth := 0
	for i := len(params) - 1; i >= 0; i-- {
		switch params[i] {
		case ')', ']', '}':
			depth++
		case '(', '[', '{':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(params[:i]), strings.TrimSpace(params[i+1:])
			}
		}
	}
	return "", strings.TrimSpace(params)
}

// Run invokes go-apidiff for baseRef and applies the policy.
//
// go-apidiff fixes its exit status before printing anything, so its output
// cannot be piped through a filter without losing the distinction between "no
// findings" and "the tool failed": a bad ref prints nothing and a naive
// pipeline then reports success. Run therefore inspects the exit status
// itself and refuses to suppress anything at or above operationalExitCode.
func Run(baseRef, repoPath string) (Result, error) {
	clone, module, cleanup, err := isolate(repoPath)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	cmd := exec.Command("go-apidiff", baseRef, "--repo-path", clone)
	cmd.Dir = module
	out, err := cmd.Output()

	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return Result{}, fmt.Errorf("running go-apidiff: %w", err)
		}
		if code := ee.ExitCode(); code >= operationalExitCode {
			return Result{}, fmt.Errorf("go-apidiff failed (exit %d): %s",
				code, strings.TrimSpace(string(ee.Stderr)))
		}
		// Exit 1 means incompatible changes were found and printed; that is
		// the report this package exists to classify.
	}
	return Filter(strings.NewReader(string(out)))
}

// isolate copies the repository to a scratch clone and returns the clone's
// root, the directory inside it corresponding to the caller's module, and a
// cleanup function.
//
// go-apidiff checks commits out in place, using go-git with Force: true, and
// that wipes every untracked and ignored file in the repository -- not merely
// in the module under test. Measured on this repository, one run destroys
// tools/japicmp.jar, spicedb-typescript/node_modules/ and spicedb-python/.venv/.
// That is survivable when the check runs alone in its own CI job, which is how
// go.yaml uses it. It is not survivable during regeneration, where every
// language's gate runs in one shared tree and Go happens to go first: the Go
// gate passes and then java fails with "japicmp not found" and typescript fails
// with "Cannot find module 'vitest'", neither of which has anything to do with
// API compatibility.
//
// A linked worktree is not an option -- go-apidiff's go-git cannot resolve
// objects through one, and fails with "object not found" -- so this clones. The
// clone is local, so git hardlinks the object store and the copy is cheap.
func isolate(repoPath string) (clone, module string, cleanup func(), err error) {
	root, err := filepath.Abs(repoPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolving repo path: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", nil, fmt.Errorf("resolving working directory: %w", err)
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return "", "", nil, fmt.Errorf("locating module within the repository: %w", err)
	}

	// The commit under test is HEAD, so the clone has to be detached at the
	// same commit: a plain clone checks out the default branch, which is not
	// necessarily where the caller is.
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", nil, fmt.Errorf("reading HEAD: %w", err)
	}

	dir, err := os.MkdirTemp("", "apicompat-")
	if err != nil {
		return "", "", nil, fmt.Errorf("creating scratch directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	clone = filepath.Join(dir, "repo")
	if out, err := exec.Command("git", "clone", "--quiet", "--no-checkout", root, clone).CombinedOutput(); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("cloning for isolation: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", clone, "checkout", "--quiet", "--detach", strings.TrimSpace(string(head))).CombinedOutput(); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("checking out %s in the clone: %w: %s",
			strings.TrimSpace(string(head)), err, strings.TrimSpace(string(out)))
	}

	return clone, filepath.Join(clone, rel), cleanup, nil
}
