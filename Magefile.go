//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var languages = []string{"go", "python", "typescript", "csharp", "java", "ruby", "rust"}

type Gen mg.Namespace

// All runs proto generation for all languages, then idiomatic client updates.
func (Gen) All() error {
	if err := (Gen{}).Proto(); err != nil {
		return err
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

func genProtoLangs(langs []string) error {
	var failures []string
	for _, l := range langs {
		dir := filepath.Join("proto-clients", fmt.Sprintf("spicedb-%s-proto", l))
		fmt.Printf("\n==> Generating proto client: %s\n", dir)

		if err := runMageIn(dir, "gen"); err != nil {
			fmt.Printf("==> FAILED: %s: %v\n", dir, err)
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

// Update runs the full update pipeline: gen, test, lint, then commits all changes.
func Update() error {
	fmt.Println("=== Step 1/4: Generate ===")
	if err := (Gen{}).All(); err != nil {
		return fmt.Errorf("generation failed: %w", err)
	}

	fmt.Println("\n=== Step 2/4: Test ===")
	if err := Test(); err != nil {
		return fmt.Errorf("tests failed: %w", err)
	}

	fmt.Println("\n=== Step 3/4: Lint ===")
	if err := (Lint{}).All(); err != nil {
		return fmt.Errorf("linting failed: %w", err)
	}

	fmt.Println("\n=== Step 4/4: Commit ===")
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

// runClaudeOutput pipes a prompt to claude and returns stdout as a string.
func runClaudeOutput(prompt string) (string, error) {
	cmd := exec.Command("claude", "--print")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return string(out), err
}

func runMageIn(dir string, target string) error {
	return sh.RunV("mage", "-d", dir, target)
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
