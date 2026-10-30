package main

import (
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
)

// Every generator documents its options, so --help never hides a flag.
func TestEveryGeneratorDocumentsItsOptions(t *testing.T) {
	if len(generator.Registry) == 0 {
		t.Fatal("no generators registered")
	}
	for name, g := range generator.Registry {
		d, ok := g.(generator.OptionDescriber)
		if !ok || len(d.Options()) == 0 {
			t.Errorf("%s generator does not describe its options", name)
			continue
		}
		for _, o := range d.Options() {
			if o.Name == "" || o.Value == "" || o.Description == "" {
				t.Errorf("%s: incomplete option description %+v", name, o)
			}
		}
	}
}

// Go, Java, TypeScript and Python list --<lang>.names-only in --help.
func TestNamesOnlyIsDescribed(t *testing.T) {
	for _, lang := range []string{"go", "java", "typescript", "python"} {
		d, ok := generator.Registry[lang].(generator.OptionDescriber)
		if !ok {
			t.Fatalf("%s generator does not describe its options", lang)
		}
		found := false
		for _, o := range d.Options() {
			found = found || o.Name == generator.NamesOnlyOption
		}
		if !found {
			t.Errorf("%s generator does not describe --%s.%s", lang, lang, generator.NamesOnlyOption)
		}
	}
}

// A bare boolean flag means true, whether it is last or followed by another flag.
func TestBareBooleanLanguageFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--lang", "go", "--go.names-only"},
		{"--go.names-only", "--out", "x.go", "--go.package=p"},
		{"--go.names-only=true", "--go.package", "p"},
	} {
		rest, langArgs := extractLangOptions(args)
		opts := filterLangOptions("go", langArgs)
		if opts["names-only"] != "true" {
			t.Errorf("%v: names-only = %q, want true (opts %v)", args, opts["names-only"], opts)
		}
		if p, ok := opts["package"]; ok && p != "p" {
			t.Errorf("%v: package = %q, want p", args, p)
		}
		for _, r := range rest {
			if r == "--go.names-only" {
				t.Errorf("%v: language flag left in standard args", args)
			}
		}
	}
}
