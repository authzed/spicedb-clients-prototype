//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/authzed/spicedb-clients/internal/clauderun"
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

func genProtoLangs(langs []string) error {
	buftag := strings.TrimSpace(os.Getenv("BUFTAG"))
	if buftag != "" {
		fmt.Printf("==> Pinning upstream API to %s\n", buftag)
	}

	var failures []string
	for _, l := range langs {
		dir := filepath.Join("proto-clients", fmt.Sprintf("spicedb-%s-proto", l))
		fmt.Printf("\n==> Generating proto client: %s\n", dir)

		env, cleanup, err := bufPinEnv(dir, l, buftag)
		if err != nil {
			// Deliberately hard-return rather than aggregating into failures like a
			// generation error does: a bad pin is bad for every language, so six
			// more identical failures add nothing.
			return fmt.Errorf("could not pin %s to %s: %w", dir, buftag, err)
		}

		genErr := runMageInWithEnv(dir, env, "gen")
		cleanup()

		if genErr != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, genErr)
			failures = append(failures, l)
			continue
		}

		// Commit proto changes
		if err := commitIfChanged(dir, fmt.Sprintf("gen: regenerate %s proto client", l)); err != nil {
			fmt.Printf("==> Warning: commit failed for %s: %v\n", dir, err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("proto generation failed for: %s", strings.Join(failures, ", "))
	}
	return nil
}

func genClientLangs(langs []string) error {
	var failures []string
	for _, l := range langs {
		dir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("\n==> Updating idiomatic client: %s\n", dir)

		if err := runMageIn(dir, "gen"); err != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, err)
			failures = append(failures, l)
			continue
		}

		// Commit idiomatic changes
		if err := commitIfChanged(dir, fmt.Sprintf("gen: update %s idiomatic client", l)); err != nil {
			fmt.Printf("==> Warning: commit failed for %s: %v\n", dir, err)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("idiomatic client update failed for: %s", strings.Join(failures, ", "))
	}
	return nil
}

func apiCompatAll(baseRef string) error {
	var failures []string
	for _, l := range apiCompatLanguages {
		dir := fmt.Sprintf("spicedb-%s", l)
		fmt.Printf("\n==> Checking API compatibility: %s\n", dir)
		if err := runMageInWithArgs(dir, "apicompat", baseRef); err != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, err)
			failures = append(failures, l)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("API compatibility check failed for: %s. Run 'mage updateAllowBreak' to proceed", strings.Join(failures, ", "))
	}
	fmt.Println("\n==> All API compatibility checks passed!")
	return nil
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
	cmd := exec.Command("claude", args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func runMageIn(dir string, target string) error {
	return sh.RunV("mage", "-d", dir, target)
}

func runMageInWithArgs(dir string, target string, args ...string) error {
	cmdArgs := append([]string{"-d", dir, target}, args...)
	return sh.RunV("mage", cmdArgs...)
}

func commitIfChanged(dir string, msg string) error {
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
