//go:build mage

package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

var languages = []string{"go", "python", "typescript"}

type Gen mg.Namespace

// All runs proto generation for all languages, then idiomatic client updates.
func (Gen) All() error {
	if err := (Gen{}).Proto(""); err != nil {
		return err
	}
	return (Gen{}).Client("")
}

// Proto regenerates proto clients. Pass a language name to run one, or empty for all.
func (Gen) Proto(lang string) error {
	langs := languages
	if lang != "" {
		langs = []string{lang}
	}

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

// Client updates idiomatic clients. Pass a language name to run one, or empty for all.
func (Gen) Client(lang string) error {
	langs := languages
	if lang != "" {
		langs = []string{lang}
	}

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

func runMageIn(dir string, target string) error {
	return sh.Run("mage", "-d", dir, target)
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
