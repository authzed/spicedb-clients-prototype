package typescript

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

// Generator implements generator.LanguageGenerator for TypeScript.
type Generator struct{}

// Language returns "typescript".
func (g *Generator) Language() string {
	return "typescript"
}

// TemplateData holds all pre-computed data needed by the template.
type TemplateData struct {
	Definitions []DefinitionData
}

// DefinitionData holds pre-computed data for a single definition.
type DefinitionData struct {
	Name       string // original name, e.g. "document"
	PascalName string // PascalCase name, e.g. "Document"
	Relations  []RelationData
	Permissions []PermissionData
	// SubRefTypes lists the (relation, ref type name) pairs for this definition
	// when it is referenced as type#relation by other definitions.
	SubRefTypes []SubRefTypeData
}

// RelationData holds pre-computed data for a single relation.
type RelationData struct {
	Name         string // original name
	SubjectUnion string // e.g. "UserRef | TeamMemberRef"
}

// PermissionData holds pre-computed data for a single permission.
type PermissionData struct {
	Name         string // original name
	SubjectUnion string // e.g. "UserRef | TeamMemberRef"
}

// SubRefTypeData represents a sub-ref type like TeamMemberRef.
type SubRefTypeData struct {
	Relation    string // e.g. "member"
	RefTypeName string // e.g. "TeamMemberRef"
}

// Generate produces generated TypeScript files from the parsed schema.
func (g *Generator) Generate(s *schema.Schema) ([]generator.GeneratedFile, error) {
	data := buildTemplateData(s)

	funcMap := template.FuncMap{
		"hasPermissions": func(d DefinitionData) bool {
			return len(d.Permissions) > 0
		},
		"hasRelations": func(d DefinitionData) bool {
			return len(d.Relations) > 0
		},
		"hasSubRefTypes": func(d DefinitionData) bool {
			return len(d.SubRefTypes) > 0
		},
		"hasRelationsOrPermissions": func(d DefinitionData) bool {
			return len(d.Relations) > 0 || len(d.Permissions) > 0
		},
	}

	tmpl, err := template.New("typed_client.ts.tmpl").Funcs(funcMap).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return []generator.GeneratedFile{
		{
			Path:    "typed_client.gen.ts",
			Content: buf.Bytes(),
		},
	}, nil
}

// buildTemplateData pre-computes all data needed by the template.
func buildTemplateData(s *schema.Schema) TemplateData {
	// First pass: collect all (definition, relation) pairs referenced as subjects.
	type subRef struct {
		definition string
		relation   string
	}
	subRefs := map[subRef]bool{}
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				}
			}
		}
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				}
			}
		}
	}

	data := TemplateData{
		Definitions: make([]DefinitionData, 0, len(s.Definitions)),
	}

	for _, def := range s.Definitions {
		dd := DefinitionData{
			Name:       def.Name,
			PascalName: toPascalCase(def.Name),
		}

		// Collect sub-ref types for this definition.
		for _, rel := range def.Relations {
			if subRefs[subRef{def.Name, rel.Name}] {
				dd.SubRefTypes = append(dd.SubRefTypes, SubRefTypeData{
					Relation:    rel.Name,
					RefTypeName: toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref",
				})
			}
		}

		for _, rel := range def.Relations {
			dd.Relations = append(dd.Relations, RelationData{
				Name:         rel.Name,
				SubjectUnion: buildSubjectUnion(rel.AllowedSubjects),
			})
		}

		for _, perm := range def.Permissions {
			dd.Permissions = append(dd.Permissions, PermissionData{
				Name:         perm.Name,
				SubjectUnion: buildSubjectUnion(perm.ReachableSubjects),
			})
		}

		data.Definitions = append(data.Definitions, dd)
	}

	return data
}

// buildSubjectUnion builds a TypeScript union type string from a list of subject types.
func buildSubjectUnion(subjects []schema.SubjectType) string {
	if len(subjects) == 0 {
		return "never"
	}

	seen := map[string]bool{}
	var parts []string
	for _, st := range subjects {
		name := subjectRefTypeName(st)
		if seen[name] {
			continue
		}
		seen[name] = true
		parts = append(parts, name)
	}
	return strings.Join(parts, " | ")
}

// subjectRefTypeName returns the TypeScript ref type name for a subject type.
func subjectRefTypeName(st schema.SubjectType) string {
	if st.Relation != "" {
		return toPascalCase(st.Definition) + toPascalCase(st.Relation) + "Ref"
	}
	return toPascalCase(st.Definition) + "Ref"
}

// toPascalCase converts a snake_case or lowercase string to PascalCase.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		result.WriteString(strings.ToUpper(p[:1]))
		result.WriteString(p[1:])
	}
	return result.String()
}
