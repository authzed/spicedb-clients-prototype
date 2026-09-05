//go:build mage

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/authzed/spicedb-clients/internal/clauderun"
	"github.com/authzed/spicedb-clients/internal/gitlock"
	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var languages = []string{"go", "python", "typescript", "csharp", "java", "ruby", "rust"}

var apiCompatLanguages = []string{"go", "python", "typescript", "csharp", "java", "rust"}

// lastGenerationFile records, per idiomatic client, the commit its last
// generation was based on. Each client diffs the proto tier against it.
const lastGenerationFile = ".last-generation"

// stampLastGeneration writes ref into every idiomatic client's baseline file.
//
// It exists because genProtoLangs commits once per language: by the time the
// idiomatic tier runs, each client's HEAD~1 fallback points at the
// second-to-last proto commit, so every language except the last one committed
// sees an empty diff and skips -- silently, with a zero exit. Stamping the real
// pre-generation SHA makes the baseline mean what its name says.
func stampLastGeneration(langs []string, ref string) error {
	for _, l := range langs {
		path := filepath.Join(fmt.Sprintf("spicedb-%s", l), lastGenerationFile)
		if err := os.WriteFile(path, []byte(ref+"\n"), 0o644); err != nil {
			return fmt.Errorf("stamp %s: %w", path, err)
		}
	}
	return nil
}

type Gen mg.Namespace

// All runs proto generation for all languages, then idiomatic client updates.
func (Gen) All() error {
	before, err := sh.Output("git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("capture pre-generation HEAD: %w", err)
	}
	before = strings.TrimSpace(before)

	if err := (Gen{}).Proto(); err != nil {
		return err
	}

	after, err := sh.Output("git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("read post-generation HEAD: %w", err)
	}

	// Only stamp when the proto tier actually committed. In gen-nodiff CI the
	// proto tier rolls its output back and commits nothing; stamping there would
	// dirty seven tracked files and fail the no-diff assertion itself.
	if strings.TrimSpace(after) != before {
		if err := stampLastGeneration(languages, before); err != nil {
			return err
		}
	}

	return (Gen{}).Client()
}

// Proto regenerates all proto clients.
func (Gen) Proto() error {
	return genProtoLangs(languages)
}

// ProtoLang regenerates a single proto client by language name (go, python, typescript, csharp, java, ruby, rust).
func (Gen) ProtoLang(lang string) error {
	return genProtoLangs([]string{lang})
}

// Client updates all idiomatic clients.
func (Gen) Client() error {
	return genClientLangs(languages)
}

// ClientLang updates a single idiomatic client by language name (go, python, typescript, csharp, java, ruby, rust).
func (Gen) ClientLang(lang string) error {
	return genClientLangs([]string{lang})
}

// defaultSummaryPath is where Summary writes when outPath is empty.
const defaultSummaryPath = ".pr-summary.md"

// Summary asks Claude to write a markdown PR-description summary of what
// changed between baseRef and HEAD, and writes it to outPath (or
// defaultSummaryPath, when outPath is empty).
//
// baseRef and outPath are both parameters rather than hardcoded, so this is
// testable and so a human can run it against any range and to any file.
// baseRef should be the pre-generation HEAD -- the same value Gen.All
// already captures for .last-generation stamping -- passed in by the
// caller rather than recomputed here, so there is exactly one notion of
// "the baseline" across the generation loop.
//
// This deliberately never sees the diff itself: PR #79 was 365 files and
// 152k lines, and pasting a diff that size would blow the context window
// exactly as spicedb-java-proto's 56,401-line diff once did ("Prompt is too
// long"). Instead it hands Claude a `git log --oneline` and a
// `git diff --stat`, both small and bounded, and tells Claude it has file
// access to read anything it wants more detail on.
func (Gen) Summary(baseRef, outPath string) error {
	return writeSummary(baseRef, outPath)
}

func writeSummary(baseRef, outPath string) error {
	outPath = resolveSummaryPath(outPath)

	logOut, err := sh.Output("git", "log", "--oneline", baseRef+"..HEAD")
	if err != nil {
		return fmt.Errorf("git log %s..HEAD: %w", baseRef, err)
	}

	statOut, err := sh.Output("git", "diff", "--stat", baseRef+"..HEAD")
	if err != nil {
		return fmt.Errorf("git diff --stat %s..HEAD: %w", baseRef, err)
	}

	if !clauderun.Available() {
		return fmt.Errorf("claude not available; cannot write a summary to %s", outPath)
	}

	fmt.Println("==> Invoking Claude to summarize the generated changes...")
	if err := clauderun.Run(summaryPrompt(logOut, statOut, outPath)); err != nil {
		return fmt.Errorf("claude invocation failed: %w", err)
	}

	return checkSummaryWritten(outPath)
}

// resolveSummaryPath returns outPath unchanged unless it is empty (or
// whitespace-only), in which case it returns defaultSummaryPath.
func resolveSummaryPath(outPath string) string {
	if strings.TrimSpace(outPath) == "" {
		return defaultSummaryPath
	}
	return outPath
}

// summaryPrompt builds the prompt asking Claude to write a PR-description
// summary of log/stat -- a `git log --oneline` and `git diff --stat` pair --
// to outPath. Claude is expected to write the file itself via its own tool
// access, the same way every per-client Magefile already asks Claude to edit
// files directly rather than parsing captured stdout: clauderun.Run streams
// output and returns only an error, so outPath is how the result comes back.
func summaryPrompt(log, stat, outPath string) string {
	return fmt.Sprintf(`You are writing the description for an automated pull request that regenerates SpiceDB's client libraries from an upstream API change.

Below is the commit log and the per-file diffstat for this regeneration (the base commit through HEAD). Do not assume this tells you everything: you have full file access in this checkout, so read any file you want more detail on before writing -- but do not try to read a full diff yourself; use the log and stat below plus targeted file reads instead.

Commits:

%s

Diffstat:

%s

Write a PR description for a reviewer who is deciding whether to merge this. Rules:

- Lead with the cross-cutting change when the same change lands in several clients. This is the common case: all seven clients track one upstream proto change, so describe a shared change once, not once per client.
- Add a per-client note only where a client did something different from the others.
- Name any public API change explicitly and prominently -- a new or changed method signature, a new parameter, a new public type. Those are what a reviewer must actually judge.
- Be brief: aim for roughly 200-400 words.
- Output ONLY the markdown body itself. No preamble, no code fence wrapping the whole thing, no "Here is the summary" framing.

Write that markdown, and nothing else, to the file at %s, overwriting it if it already exists. Do not print the summary to stdout.`,
		strings.TrimSpace(log), strings.TrimSpace(stat), outPath)
}

// checkSummaryWritten distinguishes "Claude produced nothing" (outPath was
// never created) from "Claude produced an empty file" (outPath exists but
// has no content), so a caller can tell which failure occurred rather than
// see one generic error.
func checkSummaryWritten(outPath string) error {
	data, err := os.ReadFile(outPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("claude did not write a summary to %s", outPath)
		}
		return fmt.Errorf("read summary %s: %w", outPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("claude wrote an empty summary to %s", outPath)
	}
	return nil
}

// defaultGenConcurrency is used whenever GEN_CONCURRENCY is unset, empty, or
// not a parseable integer. Chosen per the design doc's "start at 3-4 and
// tune": the runner this targets (depot-ubuntu-24.04-arm-8) has eight cores
// for seven languages' worth of heavy build/test retry loops, so 4 gives two
// rounds per tier rather than one wave of seven contending directly.
const defaultGenConcurrency = 4

// genConcurrency reads GEN_CONCURRENCY leniently: unset, empty, or anything
// that fails to parse as an integer falls back to defaultGenConcurrency
// rather than erroring, and the result is clamped to at least 1. Setting it
// to 1 reproduces today's strictly-sequential behavior exactly -- that is
// both the escape hatch if concurrency misbehaves in CI and how this change
// is validated (see the design doc's Validation section).
func genConcurrency() int {
	v := strings.TrimSpace(os.Getenv("GEN_CONCURRENCY"))
	if v == "" {
		return defaultGenConcurrency
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultGenConcurrency
	}
	if n < 1 {
		return 1
	}
	return n
}

// protoGenConcurrency bounds genProtoLangs' pool, and is deliberately not
// governed by GEN_CONCURRENCY the way the idiomatic tier is.
//
// The proto tier's Gen() calls `buf generate` / `buf export` against the Buf
// Schema Registry, which rate-limits concurrent callers. Run 33791327423
// (PR #82) ran genProtoLangs at GEN_CONCURRENCY=4: all four pool workers
// called `buf generate` within the same 200ms, the BSR returned
// `resource_exhausted: too many requests` to three of them, and six of
// seven languages failed within two seconds -- the pool worked exactly as
// designed, the BSR did not allow it. See
// https://buf.build/docs/bsr/rate-limits/.
//
// The idiomatic tier never touches the BSR (it reads local files and runs
// Claude/local builds) and is unaffected, so it keeps using
// GEN_CONCURRENCY. The proto tier is also the shorter of the two (~10min
// vs. ~35min per the design doc's measurements), so serializing it costs
// little. If the BSR's limit is ever raised, this is a one-constant change.
const protoGenConcurrency = 1

// runPool calls fn(i) for every i in [0, n), using at most concurrency
// goroutines running at once (clamped to [1, n]), and blocks until every
// call has returned. Each call's error is recorded at its own index, so a
// caller can report results in canonical (index) order regardless of which
// goroutine finished first -- fn(i) is the only thing that runs concurrently
// here; collecting and reporting results is left entirely to the caller,
// which does so strictly after runPool returns.
func runPool(n, concurrency int, fn func(i int) error) []error {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > n {
		concurrency = n
	}

	results := make([]error, n)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fn(i)
		}(i)
	}
	wg.Wait()
	return results
}

// protoClientDirFor returns the proto-clients directory for a language, the
// same computation genProtoLangs and bufPinEnv both need.
func protoClientDirFor(lang string) string {
	return filepath.Join("proto-clients", fmt.Sprintf("spicedb-%s-proto", lang))
}

// failedLangs returns the subset of langs whose corresponding entry in
// results is a non-nil error, in langs' order. Shared by genProtoLangs and
// genClientLangs so both build their aggregate failure message -- which the
// regen-from-api workflow greps for verbatim -- in canonical order
// regardless of which language's worker actually finished first.
func failedLangs(langs []string, results []error) []string {
	var failed []string
	for i, l := range langs {
		if results[i] != nil {
			failed = append(failed, l)
		}
	}
	return failed
}

// genProtoLangs runs proto generation for every language in langs, then
// commits each success serially, in langs' order.
//
// This is a two-phase split, not a single concurrent loop, because git
// writes cannot be allowed to interleave with generation: phase one
// (generate) runs the whole language list through a bounded worker pool with
// no git operations of its own in this loop -- each Gen() still shells out to
// git internally, but that is serialized by internal/gitlock, not by this
// function. Phase two (commit) then runs strictly after every worker has
// finished, iterating langs in its declared order rather than completion
// order, so a regeneration produces the same commit sequence run to run
// regardless of which language happened to finish first.
//
// The pool here still runs through runPool, the same code as genClientLangs,
// but bounded by protoGenConcurrency rather than genConcurrency(): this tier
// hits the Buf Schema Registry and is rate-limited by it (see
// protoGenConcurrency's doc comment), so it stays serial regardless of
// GEN_CONCURRENCY. Using the pool at concurrency 1 rather than a hand-rolled
// serial loop costs nothing and keeps one code path for both tiers.
func genProtoLangs(langs []string) error {
	buftag := strings.TrimSpace(os.Getenv("BUFTAG"))
	if buftag != "" {
		fmt.Printf("==> Pinning upstream API to %s\n", buftag)
	}

	// pinErrs is populated directly by each worker (each goroutine only ever
	// writes its own index, so this needs no separate synchronization), and
	// checked before any commit happens. This is intentionally not part of
	// the results slice runPool returns: a bad pin must hard-abort the whole
	// run rather than aggregate like an ordinary generation failure -- see
	// the check just after runPool below.
	pinErrs := make([]error, len(langs))

	results := runPool(len(langs), protoGenConcurrency, func(i int) error {
		l := langs[i]
		dir := protoClientDirFor(l)
		fmt.Printf("\n==> Generating proto client: %s\n", dir)

		env, cleanup, err := bufPinEnv(dir, l, buftag)
		if err != nil {
			// A bad pin is bad for every language, so this is recorded
			// separately from an ordinary generation failure and hard-aborts the
			// run below rather than aggregating alongside five other identical
			// failures. It happens inside a worker now, so it cannot simply
			// return early out of genProtoLangs the way the sequential version
			// did -- the other workers already dispatched keep running, and the
			// check below is what stops the run from proceeding to commit.
			pinErrs[i] = fmt.Errorf("could not pin %s to %s: %w", dir, buftag, err)
			return err
		}

		// Streamed rather than buffered like the idiomatic tier: this pool is
		// bounded to protoGenConcurrency workers, so nothing interleaves and
		// live output is worth more than collapsibility. Raising that bound
		// means adopting genClientLangs' langLog here too.
		genErr := runMageInWithEnv(dir, env, "gen")
		cleanup()

		if genErr != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, genErr)
		}
		return genErr
	})

	for _, err := range pinErrs {
		if err != nil {
			return err
		}
	}

	failures := failedLangs(langs, results)
	for i, l := range langs {
		if results[i] != nil {
			continue
		}
		dir := protoClientDirFor(l)
		if err := commitIfChanged(dir, fmt.Sprintf("gen: regenerate %s proto client", l)); err != nil {
			fmt.Printf("==> Warning: commit failed for %s: %v\n", dir, err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("proto generation failed for: %s", strings.Join(failures, ", "))
	}
	return nil
}

// genClientLangs runs the idiomatic client update for every language in
// langs, then commits each success serially, in langs' order. See
// genProtoLangs for why generation and commit are two separate phases rather
// than one concurrent loop.
//
// Unlike genProtoLangs, this tier's pool is bounded by genConcurrency()
// (GEN_CONCURRENCY), not protoGenConcurrency: Gen() here reads local files
// and runs Claude/local builds, never the Buf Schema Registry, so it isn't
// subject to the BSR rate limit that forces the proto tier to stay serial.
func genClientLangs(langs []string) error {
	log := &langLog{}
	prog := newProgress()
	stop := heartbeat(heartbeatInterval, prog.running)
	defer stop()

	results := runPool(len(langs), genConcurrency(), func(i int) error {
		l := langs[i]
		dir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("==> Updating idiomatic client: %s\n", dir)

		prog.begin(dir)
		out, err := runMageInQuiet(dir, "gen")
		prog.done(dir)

		log.flush(dir, out, err != nil)
		if err != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, err)
			return err
		}
		return nil
	})

	failures := failedLangs(langs, results)
	for i, l := range langs {
		if results[i] != nil {
			continue
		}
		dir := fmt.Sprintf("spicedb-%s", l)
		if err := commitIfChanged(dir, fmt.Sprintf("gen: update %s idiomatic client", l)); err != nil {
			fmt.Printf("==> Warning: commit failed for %s: %v\n", dir, err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("idiomatic client update failed for: %s", strings.Join(failures, ", "))
	}
	return nil
}

// maxCompatRetries bounds how many times Claude is asked to repair an API
// compatibility break before the run gives up. It matches the build/test retry
// budget each client's Gen target already uses.
const maxCompatRetries = 3

// apiCompatBreak is one language's failing compatibility report.
type apiCompatBreak struct {
	lang   string
	report string
}

// apiCompatOnce runs every language's apiCompat target once, returning the
// languages that failed along with what they printed.
func apiCompatOnce(baseRef string) []apiCompatBreak {
	var breaks []apiCompatBreak
	for _, l := range apiCompatLanguages {
		dir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("\n==> Checking API compatibility: %s\n", dir)
		out, err := runMageInCapture(dir, "apicompat", baseRef)
		if err != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, err)
			breaks = append(breaks, apiCompatBreak{lang: l, report: out})
		}
	}
	return breaks
}

// apiCompatAll checks every language against baseRef, giving Claude the
// failing reports and a chance to repair them.
//
// Regeneration widens the client surface whenever the upstream API gains
// something, and that routinely breaks compatibility. Until now such a break
// failed the whole run here at step 2 -- long after Claude had finished
// generating, and with nothing telling it what went wrong, so the same break
// came back on the next run.
//
// The repair is committed between attempts, which is also why this loop lives
// here rather than inside each client's Gen target. go-apidiff compares git
// commits and refuses to run against a dirty worktree at all ("current git tree
// is dirty", exit 2), so inside Gen -- where Claude's edits are still
// uncommitted -- the check could not run, and an uncommitted repair here would
// not merely go unmeasured, it would make the next attempt fail for an
// unrelated reason.
func apiCompatAll(baseRef string) error {
	for attempt := 1; attempt <= maxCompatRetries; attempt++ {
		breaks := apiCompatOnce(baseRef)
		if len(breaks) == 0 {
			fmt.Println("\n==> All API compatibility checks passed!")
			return nil
		}

		langs := make([]string, len(breaks))
		for i, b := range breaks {
			langs[i] = b.lang
		}
		joined := strings.Join(langs, ", ")

		if !clauderun.Available() {
			return fmt.Errorf("API compatibility check failed for: %s. Run 'mage updateAllowBreak' to proceed", joined)
		}
		if attempt == maxCompatRetries {
			return fmt.Errorf("API compatibility check failed for: %s after %d repair attempts. Run 'mage updateAllowBreak' to proceed", joined, maxCompatRetries)
		}

		fmt.Printf("\n==> API compatibility broke in %s; asking Claude to fix (attempt %d/%d)...\n", joined, attempt, maxCompatRetries)
		if err := clauderun.Run(apiCompatFixPrompt(breaks)); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}

		before, err := sh.Output("git", "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("reading HEAD before the compatibility fix: %w", err)
		}
		if err := commitIfChanged(".", fmt.Sprintf("fix: restore API compatibility in %s", joined)); err != nil {
			return fmt.Errorf("committing the compatibility fix: %w", err)
		}
		after, err := sh.Output("git", "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("reading HEAD after the compatibility fix: %w", err)
		}
		// Claude edited nothing, so the next attempt would re-run an identical
		// check and fail identically. Stop now and say why, rather than
		// spending the remaining attempts to reach the same place.
		if strings.TrimSpace(before) == strings.TrimSpace(after) {
			return fmt.Errorf("API compatibility check failed for: %s, and the repair changed nothing. Run 'mage updateAllowBreak' to proceed", joined)
		}
	}
	return nil
}

// Gates runs the post-generation gates: API compatibility for every language
// that has a check, then the repo-wide markdown lint. Each repairs through
// Claude before it gives up.
//
// This is a separate target from gen:all because regeneration runs the two as
// distinct CI steps. Generation is continue-on-error there, so that a partial
// failure still yields a PR carrying whatever succeeded -- and the gates have
// to run against that same tree either way, which they cannot do from inside
// gen:all. update() calls the same two gates for local runs.
//
// Both gates run even when the first fails, so one pass reports everything a
// reviewer has to deal with rather than only the first problem.
func Gates(baseRef string) error {
	var failures []string

	fmt.Println("=== Gate 1/2: API compatibility ===")
	if err := apiCompatAll(baseRef); err != nil {
		fmt.Printf("==> GATE FAILED (API compatibility): %v\n", err)
		failures = append(failures, "API compatibility")
	}

	fmt.Println("\n=== Gate 2/2: Markdown lint ===")
	if err := markdownLintWithFix(); err != nil {
		fmt.Printf("==> GATE FAILED (markdown lint): %v\n", err)
		failures = append(failures, "markdown lint")
	}

	if len(failures) > 0 {
		return fmt.Errorf("post-generation gates failed: %s", strings.Join(failures, ", "))
	}
	fmt.Println("\n==> All post-generation gates passed!")
	return nil
}

// markdownLintTool is the repo-wide markdown linter. CI runs it bare, so the
// file set comes entirely from .markdownlint-cli2.yaml rather than from
// arguments -- its `globs` key overrides anything passed on the command line.
const markdownLintTool = "markdownlint-cli2"

// maxMarkdownRetries bounds how many times Claude is asked to clear the
// markdown gate.
const maxMarkdownRetries = 3

// runMarkdownLint runs the linter once over the whole repository, optionally
// letting it repair what it can, and returns its combined output.
func runMarkdownLint(fix bool) (string, error) {
	if _, err := exec.LookPath(markdownLintTool); err != nil {
		return "", fmt.Errorf("%s not found. Install with: npm install -g %s", markdownLintTool, markdownLintTool)
	}
	var args []string
	if fix {
		args = append(args, "--fix")
	}
	cmd := exec.Command(markdownLintTool, args...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.String(), err
}

// MarkdownLint checks every markdown file in the repository. It mirrors the
// markdown-lint CI job exactly -- same tool, same config, no arguments -- so a
// pass here means a pass there.
func MarkdownLint() error {
	fmt.Println("==> Linting Markdown (repo-wide)...")
	if _, err := runMarkdownLint(false); err != nil {
		return fmt.Errorf("markdown lint failed: %w", err)
	}
	fmt.Println("==> Markdown lint passed!")
	return nil
}

// markdownLintWithFix is the regeneration gate: check, then repair.
//
// It runs once for the whole repository rather than once per client. The rules
// are the same for every .md file, the file set lives in a single config, and
// the clients share files, so seven runs would re-lint the same content seven
// times.
//
// Unlike the API-compat gate this needs no commit between attempts, because
// markdownlint reads the working tree rather than git history.
//
// The tool repairs the mechanical rules itself -- hard tabs, blank lines around
// headings, list markers -- and those are most of what regeneration produces,
// so --fix runs before Claude is involved and Claude sees only what survived.
func markdownLintWithFix() error {
	fmt.Println("\n==> Linting Markdown (repo-wide)...")
	out, err := runMarkdownLint(false)
	if err == nil {
		fmt.Println("==> Markdown lint passed!")
		return nil
	}

	fmt.Println("==> Markdown lint failed; applying --fix...")
	out, err = runMarkdownLint(true)
	if err == nil {
		fmt.Println("==> Markdown lint passed after --fix.")
		return nil
	}

	for attempt := 1; attempt <= maxMarkdownRetries; attempt++ {
		if !clauderun.Available() {
			return fmt.Errorf("markdown lint failed and claude is unavailable:\n%s", strings.TrimSpace(out))
		}
		if attempt == maxMarkdownRetries {
			return fmt.Errorf("markdown lint still failing after %d repair attempts:\n%s", maxMarkdownRetries, strings.TrimSpace(out))
		}

		fmt.Printf("==> Asking Claude to fix the remaining markdown violations (attempt %d/%d)...\n", attempt, maxMarkdownRetries)
		if err := clauderun.Run(markdownLintFixPrompt(out)); err != nil {
			return fmt.Errorf("claude fix invocation failed: %w", err)
		}

		out, err = runMarkdownLint(false)
		if err == nil {
			fmt.Println("==> Markdown lint passed!")
			return nil
		}
	}
	return fmt.Errorf("markdown lint still failing:\n%s", strings.TrimSpace(out))
}

// markdownLintFixPrompt renders the surviving violations for Claude.
func markdownLintFixPrompt(report string) string {
	return fmt.Sprintf(
		"markdownlint-cli2 still reports these violations after `--fix` resolved everything "+
			"it could mechanically, so each one needs a judgement call.\n\n```\n%s\n```\n\n"+
			"Fix each violation in the file it names. Keep the prose intact: reflow or "+
			"restructure only as far as the rule requires, and never delete content to satisfy "+
			"a rule.\n\n"+
			"Do not add inline `<!-- markdownlint-disable -->` comments, and do not edit "+
			".markdownlint-cli2.yaml to silence a rule.",
		strings.TrimSpace(report))
}

// apiCompatFixPrompt renders the failing reports, and the rules for repairing
// them, for Claude.
func apiCompatFixPrompt(breaks []apiCompatBreak) string {
	var b strings.Builder
	b.WriteString("Regenerating the clients broke API compatibility with the previous commit. ")
	b.WriteString("Each report below is that language's `mage apicompat` output.\n\n")
	for _, br := range breaks {
		fmt.Fprintf(&b, "### spicedb-%s\n\n```\n%s\n```\n\n", br.lang, strings.TrimSpace(br.report))
	}
	b.WriteString("Fix each break by preserving the existing public API. Prefer an additive " +
		"change: keep the old signature working and add the new capability alongside it -- an " +
		"optional or variadic parameter, an overload, or a separate method -- rather than " +
		"changing or removing what callers already use. Retyping or removing an exported " +
		"symbol is a last resort.\n\n" +
		"Record what you change in that client's CHANGELOG.md, under the single existing " +
		"`## Unreleased` heading -- never add a second one. Put the entry in the right `###` " +
		"subsection (`Added`, `Changed`, `Fixed`), formatted like the entries already there: " +
		"a bold `**YYYY-MM-DD: one-line summary.**` followed by an indented paragraph saying " +
		"what changed and why it matters to a caller.\n\n" +
		"Do not weaken, skip or disable the compatibility check itself to make it pass.")
	return b.String()
}

type Lint mg.Namespace

// All runs linters across all idiomatic clients.
func (Lint) All() error {
	var failures []string
	for _, l := range languages {
		dir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("\n==> Linting: %s\n", dir)
		if err := runMageIn(dir, "lint"); err != nil {
			failures = append(failures, dir)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("linting failed in: %s", strings.Join(failures, ", "))
	}
	fmt.Println("\n==> All linters passed!")
	return nil
}

// Test runs all tests across all clients.
func Test() error {
	var failures []string

	for _, l := range languages {
		// Proto client tests
		protoDir := filepath.Join("proto-clients", fmt.Sprintf("spicedb-%s-proto", l))
		fmt.Printf("\n==> Testing: %s\n", protoDir)
		if err := runMageIn(protoDir, "test"); err != nil {
			failures = append(failures, protoDir)
		}

		// Idiomatic client tests
		clientDir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("\n==> Testing: %s\n", clientDir)
		if err := runMageIn(clientDir, "test"); err != nil {
			failures = append(failures, clientDir)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("tests failed in: %s", strings.Join(failures, ", "))
	}
	fmt.Println("\n==> All tests passed!")
	return nil
}

// Update runs the full update pipeline: gen, API compat check, test, lint, then commits all changes.
func Update() error {
	return update(false)
}

// UpdateAllowBreak runs the full update pipeline without API compatibility checks.
func UpdateAllowBreak() error {
	return update(true)
}

func update(allowBreak bool) error {
	// Capture baseline SHA before gen runs
	baseRef, err := sh.Output("git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get baseline SHA: %w", err)
	}
	baseRef = strings.TrimSpace(baseRef)

	fmt.Println("=== Step 1/5: Generate ===")
	if err := (Gen{}).All(); err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	if !allowBreak {
		fmt.Println("\n=== Step 2/5: API Compatibility Check ===")
		if err := apiCompatAll(baseRef); err != nil {
			return err
		}
	} else {
		fmt.Println("\n=== Step 2/5: API Compatibility Check (skipped — breaking changes allowed) ===")
	}

	fmt.Println("\n=== Step 3/5: Test ===")
	if err := Test(); err != nil {
		return fmt.Errorf("tests failed: %w", err)
	}

	fmt.Println("\n=== Step 4/5: Lint ===")
	if err := (Lint{}).All(); err != nil {
		return fmt.Errorf("linting failed: %w", err)
	}
	if err := markdownLintWithFix(); err != nil {
		return err
	}

	fmt.Println("\n=== Step 5/5: Commit ===")
	status, err := sh.Output("git", "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		fmt.Println("==> No changes to commit.")
		return nil
	}
	if err := sh.RunV("git", "add", "-A"); err != nil {
		return err
	}

	// Ask Claude to generate a commit message from the staged diff
	diff, err := sh.Output("git", "diff", "--cached", "--stat")
	if err != nil {
		return err
	}
	fmt.Println("==> Generating commit message with Claude...")
	prompt := fmt.Sprintf(
		"Generate a concise git commit message (subject line + optional body) for these staged changes. "+
			"Output ONLY the commit message text, nothing else.\n\n%s", diff,
	)
	msg, err := runClaudeOutput(prompt)
	if err != nil || strings.TrimSpace(msg) == "" {
		// Fall back to a generic message if Claude fails
		msg = "chore: update generated clients"
	}

	if err := sh.RunV("git", "commit", "-m", strings.TrimSpace(msg)); err != nil {
		return err
	}
	fmt.Println("\n==> Update complete. Changes committed (not pushed).")
	return nil
}

// claudePermissionArgs returns the extra CLI arguments runClaudeOutput must
// append when CI_REGENERATION is set.
//
// CI_REGENERATION is set only by .github/workflows/regen-from-api.yaml, the
// one workflow holding Claude credentials. Under it, --permission-mode
// bypassPermissions is required: without it, headless Claude replies that it
// lacks write permission, edits nothing, and still exits 0 -- so a
// regeneration reports success and produces an empty PR (observed on runs
// 33010304938 vs 33010790114). --print itself is unaffected by this gate:
// runClaudeOutput already passes it unconditionally, since its caller parses
// stdout as the generated commit message whether this runs locally or in CI.
//
// The bypass is confined to that workflow, which runs on an ephemeral
// single-purpose container.
func claudePermissionArgs() []string {
	if os.Getenv("CI_REGENERATION") == "" {
		return nil
	}
	return []string{"--permission-mode", "bypassPermissions"}
}

// runClaudeOutput pipes a prompt to claude and returns stdout as a string.
func runClaudeOutput(prompt string) (string, error) {
	args := append([]string{"--print"}, claudePermissionArgs()...)
	args = append(args, clauderun.ModelArgs()...)
	cmd := exec.Command("claude", args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func runMageIn(dir string, target string) error {
	return sh.RunV("mage", "-d", dir, target)
}

// runMageInQuiet runs a mage target and returns its combined output without
// streaming it, leaving the caller to decide when to print. That choice is what
// keeps a parallel phase readable: four workers writing to one stdout produce a
// log in which no language's story can be followed end to end.
func runMageInQuiet(dir string, target string, args ...string) (string, error) {
	cmdArgs := append([]string{"-d", dir, target}, args...)
	cmd := exec.Command("mage", cmdArgs...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// langLog serialises per-language output so that concurrently generated
// languages are printed one whole block at a time rather than interleaved.
type langLog struct{ mu sync.Mutex }

// flush prints one language's output as a single uninterrupted block.
//
// A success is wrapped in a GitHub Actions group, which renders collapsed, so
// the log reads as a list of languages to expand on demand. A failure is left
// ungrouped: the reason a run failed should be visible without anyone having to
// click into it.
func (l *langLog) flush(name, out string, failed bool) {
	block := logBlock(name, out, failed, os.Getenv("GITHUB_ACTIONS") == "true")
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Print(block)
}

// logBlock renders one language's captured output. It is separate from flush so
// the formatting decisions can be tested without capturing stdout.
func logBlock(name, out string, failed, inActions bool) string {
	body := strings.TrimRight(out, "\n")
	switch {
	case failed:
		return fmt.Sprintf("\n==> %s FAILED\n%s\n", name, body)
	case inActions:
		return fmt.Sprintf("::group::%s\n%s\n::endgroup::\n", name, body)
	default:
		return fmt.Sprintf("\n==> %s\n%s\n", name, body)
	}
}

// progress tracks which languages are in flight, so a long phase can report
// what it is waiting on rather than merely that it is alive.
type progress struct {
	mu    sync.Mutex
	start map[string]time.Time
}

func newProgress() *progress { return &progress{start: map[string]time.Time{}} }

func (p *progress) begin(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.start[name] = time.Now()
}

func (p *progress) done(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.start, name)
}

// running renders the in-flight languages with how long each has been going,
// sorted longest-first so whatever is holding the phase up reads first.
func (p *progress) running() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	type entry struct {
		name string
		age  time.Duration
	}
	entries := make([]entry, 0, len(p.start))
	for name, t := range p.start {
		entries = append(entries, entry{name, time.Since(t)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].age > entries[j].age })
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = fmt.Sprintf("%s (%s)", e.name, e.age.Round(time.Second))
	}
	return out
}

// heartbeat reports what is still running every interval until the returned
// stop function is called. With per-language output buffered, this is the only
// liveness signal a long phase emits -- and naming the languages makes it
// useful for more than that, since a language stuck for twenty minutes is
// indistinguishable from a healthy one if all you print is a dot.
func heartbeat(interval time.Duration, running func() []string) (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		started := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if r := running(); len(r) > 0 {
					fmt.Printf("==> still running after %s: %s\n",
						time.Since(started).Round(time.Second), strings.Join(r, ", "))
				}
			}
		}
	}()
	return func() { close(done) }
}

// heartbeatInterval is how often a generation phase reports what it is waiting
// on. Short enough that a watcher can tell the job is alive, long enough that
// it adds a handful of lines to a forty-minute run rather than hundreds.
const heartbeatInterval = 30 * time.Second

// runMageInCapture runs a mage target and returns its combined output while
// still streaming it. A failing target's report has to reach Claude verbatim,
// and sh.RunV only streams, so this keeps both: the operator still watches the
// log live, and the text survives for the prompt.
func runMageInCapture(dir string, target string, args ...string) (string, error) {
	cmdArgs := append([]string{"-d", dir, target}, args...)
	cmd := exec.Command("mage", cmdArgs...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.String(), err
}

// commitIfChanged is the root Magefile's one direct user of git, and the
// only point where concurrently-generated languages' changes actually reach
// the index. It always ran after generation finished for its language, so it
// never contended before; now that genProtoLangs/genClientLangs run several
// languages' Gen() concurrently, this can overlap with another language's
// own internal git reads and rollback writes, so it goes through gitlock the
// same as they do.
func commitIfChanged(dir string, msg string) error {
	return gitlock.Do(func() error {
		status, err := sh.Output("git", "status", "--porcelain", dir)
		if err != nil {
			return err
		}
		if strings.TrimSpace(status) == "" {
			return nil // nothing to commit
		}
		if err := sh.Run("git", "add", dir); err != nil {
			return err
		}
		return sh.Run("git", "commit", "-m", msg)
	})
}

// authzedAPIInput is the exact buf.gen.yaml line naming the upstream API module.
// It is byte-identical in all six templates that declare it.
const authzedAPIInput = "- module: buf.build/authzed/api"

// bufGenTemplate writes a copy of dir/buf.gen.yaml with the buf.build/authzed/api
// input pinned to ref, and returns the path of that temp file. The caller owns
// removing it.
//
// Every other line is copied verbatim. That is the entire point: a positional
// `buf generate <input>` argument would be simpler, but buf documents that "the
// inputs here are ignored if an input is specified as a command line argument",
// which would silently drop spicedb-python-proto's protovalidate input and
// spicedb-typescript-proto's googleapis input -- the latter being what lets the
// idiomatic client decode google.rpc.ErrorInfo rather than parse error strings.
//
// Returns an error rather than an unpinned template when the authzed input is
// absent: generating unpinned after the caller explicitly asked for a pin is the
// dangerous outcome, not a recoverable one.
func bufGenTemplate(dir string, ref string) (string, error) {
	src := filepath.Join(dir, "buf.gen.yaml")
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", src, err)
	}

	lines := strings.Split(string(data), "\n")
	pinned := 0
	for i, line := range lines {
		if strings.TrimSpace(line) != authzedAPIInput {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		lines[i] = indent + authzedAPIInput + ":" + ref
		pinned++
	}
	if pinned == 0 {
		return "", fmt.Errorf("%s declares no %q input to pin", src, authzedAPIInput)
	}

	f, err := os.CreateTemp("", "buf.gen.*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp template: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n")); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write temp template: %w", err)
	}
	return f.Name(), nil
}

// bufPinEnv returns the environment a child proto-client mage run needs in order
// to generate against a pinned upstream API revision, plus a cleanup func the
// caller must invoke once the child has finished.
//
// An empty ref returns an empty environment and a no-op cleanup, which is exactly
// the behavior before pinning existed -- a developer running `mage gen:all`
// without BUFTAG set sees no change.
//
// spicedb-rust-proto has no buf.gen.yaml; it runs `buf export` against a single
// positional input, so it takes the ref directly and pins the argument itself.
// The other six pin via a rewritten template.
func bufPinEnv(dir string, lang string, ref string) (map[string]string, func(), error) {
	noop := func() {}
	if ref == "" {
		return nil, noop, nil
	}
	if lang == "rust" {
		return map[string]string{"BUFTAG": ref}, noop, nil
	}

	tmpl, err := bufGenTemplate(dir, ref)
	if err != nil {
		return nil, noop, err
	}
	return map[string]string{"BUF_TEMPLATE": tmpl}, func() { _ = os.Remove(tmpl) }, nil
}

func runMageInWithEnv(dir string, env map[string]string, target string) error {
	return sh.RunWithV(env, "mage", "-d", dir, target)
}
