package python

import (
	"bytes"
	"embed"
	"sort"
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
type TemplateData struct {
	CaveatContexts []CaveatContextData
}

type CaveatContextData struct {
	ClassName  string         // e.g. "IpRangeContext"
	CaveatName string         // e.g. "ip_range"
	Params     []CaveatParamData
}

type CaveatParamData struct {
	FieldName string // snake_case, e.g. "allowed_cidr"
	PyType    string // e.g. "str"
}

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

func buildTemplateData(s *schema.Schema) TemplateData {
	usedCaveats := collectUsedCaveats(s)

	var caveatContexts []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		cc := CaveatContextData{
			ClassName:  toPascalCase(c.Name) + "Context",
			CaveatName: c.Name,
		}
		// Sort params alphabetically for deterministic output (matches Go generator).
		params := append([]schema.CaveatParam(nil), c.Params...)
		sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
		for _, p := range params {
			cc.Params = append(cc.Params, CaveatParamData{
				FieldName: escapeKeyword(p.Name),
				PyType:    celTypeToPyType(p.Type),
			})
		}
		caveatContexts = append(caveatContexts, cc)
	}

	return TemplateData{CaveatContexts: caveatContexts}
}

func collectUsedCaveats(s *schema.Schema) map[string]bool {
	used := map[string]bool{}
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.CaveatName != "" {
					used[st.CaveatName] = true
				}
			}
		}
	}
	return used
}

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
