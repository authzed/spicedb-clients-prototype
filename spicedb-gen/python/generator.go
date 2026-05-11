package python

import (
	"bytes"
	"embed"
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
