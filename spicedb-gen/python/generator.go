package python

import (
	"bytes"
	"embed"
	"strings"
	"text/template"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

func init() {
	generator.Register(&Generator{})
}

// Generator implements generator.LanguageGenerator for Python.
type Generator struct{}

// Language returns "python".
func (g *Generator) Language() string { return "python" }

// TemplateData holds all pre-computed data needed by the Python template.
// Fields are filled in by later tasks.
type TemplateData struct{}

// Generate produces a permissions.py file from the parsed schema.
// opts is currently unused.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	data := buildTemplateData(s)

	tmpl, err := template.New("typed_client.py.tmpl").ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return []generator.GeneratedFile{
		{Path: "permissions.py", Content: buf.Bytes()},
	}, nil
}

func buildTemplateData(_ *schema.Schema) TemplateData { return TemplateData{} }

// pyKeywords are Python reserved words plus selected built-in names we should
// not clobber when generating identifiers. Hits get a trailing underscore.
var pyKeywords = map[string]bool{
	// Python language keywords (per the language reference / keyword.kwlist)
	"False": true, "None": true, "True": true,
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
	// Soft keywords (3.10+)
	"match": true, "case": true,
	// Builtins commonly shadowed
	"id": true, "type": true, "list": true, "dict": true, "set": true,
	"str": true, "int": true, "float": true, "bool": true, "bytes": true,
}

// escapeKeyword appends "_" if name would shadow a Python keyword / common builtin.
func escapeKeyword(name string) string {
	if pyKeywords[name] {
		return name + "_"
	}
	return name
}

// toPascalCase converts snake_case to PascalCase.
func toPascalCase(s string) string {
	var b strings.Builder
	for _, part := range strings.Split(s, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// toSnakeCase converts snake_case or CamelCase to snake_case.
func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// celTypeToPyType maps a CEL primitive type to its Python equivalent.
func celTypeToPyType(celType string) string {
	switch celType {
	case "string", "duration", "timestamp":
		return "str"
	case "int", "uint":
		return "int"
	case "double":
		return "float"
	case "bool":
		return "bool"
	default:
		return "Any"
	}
}
