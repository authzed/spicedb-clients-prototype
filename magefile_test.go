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
