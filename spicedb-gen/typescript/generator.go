package typescript

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

// Generator implements generator.LanguageGenerator for TypeScript.
type Generator struct{}

// Language returns "typescript".
func (g *Generator) Language() string {
	return "typescript"
}

// TemplateData holds all pre-computed data needed by the template.
type TemplateData struct {
	Definitions         []DefinitionData
	CaveatContexts      []CaveatContextData
	CaveatedSubjectTypes []CaveatedSubjectTypeData
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
	// CaveatMethods lists withCaveat methods on the factory function.
	CaveatMethods []CaveatMethodData
}

// RelationData holds pre-computed data for a single relation.
type RelationData struct {
	Name         string // original name
	SubjectUnion string // e.g. "UserRef | TeamMemberRef"
	IsSubRef     bool   // true if this relation is referenced as type#relation elsewhere
	SubRefTypeName string // e.g. "TeamMemberRef" — only set if IsSubRef is true
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

// CaveatContextData represents a caveat context interface.
type CaveatContextData struct {
	InterfaceName string              // e.g. "IpRangeContext"
	CaveatName    string              // e.g. "ip_range"
	Fields        []CaveatFieldData   // e.g. [{Name: "allowedCidr", TSType: "string"}]
}

// CaveatFieldData represents a single field of a caveat context interface.
type CaveatFieldData struct {
	Name   string // camelCase name, e.g. "allowedCidr"
	TSType string // TypeScript type, e.g. "string"
}

// CaveatedSubjectTypeData represents a caveated subject variant type.
type CaveatedSubjectTypeData struct {
	TypeName      string // e.g. "UserIpRangeRef"
	Definition    string // e.g. "user"
	CaveatName    string // e.g. "ip_range"
	ContextType   string // e.g. "IpRangeContext"
	HasRelation   bool   // true if this is a caveated sub-ref (type#relation with caveat)
	Relation      string // e.g. "member" — only set if HasRelation
}

// CaveatMethodData represents a withCaveat method on a factory function.
type CaveatMethodData struct {
	MethodName    string // e.g. "withIpRange"
	ContextType   string // e.g. "IpRangeContext"
	ReturnType    string // e.g. "UserIpRangeRef"
	CaveatName    string // e.g. "ip_range"
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
		"hasCaveatMethods": func(d DefinitionData) bool {
			return len(d.CaveatMethods) > 0
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

	// Collect all caveated subject references: (definition, caveatName) pairs.
	type caveatRef struct {
		definition string
		relation   string // may be empty
		caveatName string
	}
	caveatRefs := map[caveatRef]bool{}

	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				}
				if st.CaveatName != "" {
					caveatRefs[caveatRef{st.Definition, st.Relation, st.CaveatName}] = true
				}
			}
		}
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				}
				if st.CaveatName != "" {
					caveatRefs[caveatRef{st.Definition, st.Relation, st.CaveatName}] = true
				}
			}
		}
	}

	// Build caveat context interfaces.
	usedCaveats := map[string]bool{}
	for cr := range caveatRefs {
		usedCaveats[cr.caveatName] = true
	}

	var caveatContexts []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		cc := CaveatContextData{
			InterfaceName: toPascalCase(c.Name) + "Context",
			CaveatName:    c.Name,
		}
		for _, p := range c.Params {
			cc.Fields = append(cc.Fields, CaveatFieldData{
				Name:   toCamelCase(p.Name),
				TSType: celTypeToTS(p.Type),
			})
		}
		caveatContexts = append(caveatContexts, cc)
	}

	// Build caveated subject variant types.
	var caveatedTypes []CaveatedSubjectTypeData
	seenCaveated := map[string]bool{}
	for cr := range caveatRefs {
		st := schema.SubjectType{Definition: cr.definition, Relation: cr.relation, CaveatName: cr.caveatName}
		typeName := caveatedSubjectTypeName(st)
		if seenCaveated[typeName] {
			continue
		}
		seenCaveated[typeName] = true
		caveatedTypes = append(caveatedTypes, CaveatedSubjectTypeData{
			TypeName:    typeName,
			Definition:  cr.definition,
			CaveatName:  cr.caveatName,
			ContextType: toPascalCase(cr.caveatName) + "Context",
			HasRelation: cr.relation != "",
			Relation:    cr.relation,
		})
	}

	// Sort caveated types for deterministic output.
	sort.Slice(caveatedTypes, func(i, j int) bool {
		return caveatedTypes[i].TypeName < caveatedTypes[j].TypeName
	})

	// Build caveat methods per definition: for definitions used as subjects with caveats.
	// Map definition name -> list of caveat methods.
	defCaveatMethods := map[string][]CaveatMethodData{}
	for cr := range caveatRefs {
		if cr.relation != "" {
			continue // caveat methods only on the base definition factory, not sub-refs
		}
		st := schema.SubjectType{Definition: cr.definition, CaveatName: cr.caveatName}
		methodName := "with" + toPascalCase(cr.caveatName)
		returnType := caveatedSubjectTypeName(st)
		defCaveatMethods[cr.definition] = append(defCaveatMethods[cr.definition], CaveatMethodData{
			MethodName:  methodName,
			ContextType: toPascalCase(cr.caveatName) + "Context",
			ReturnType:  returnType,
			CaveatName:  cr.caveatName,
		})
	}

	// Sort caveat methods for deterministic output.
	for k := range defCaveatMethods {
		sort.Slice(defCaveatMethods[k], func(i, j int) bool {
			return defCaveatMethods[k][i].MethodName < defCaveatMethods[k][j].MethodName
		})
	}

	data := TemplateData{
		Definitions:          make([]DefinitionData, 0, len(s.Definitions)),
		CaveatContexts:       caveatContexts,
		CaveatedSubjectTypes: caveatedTypes,
	}

	for _, def := range s.Definitions {
		dd := DefinitionData{
			Name:          def.Name,
			PascalName:    toPascalCase(def.Name),
			CaveatMethods: defCaveatMethods[def.Name],
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
			rd := RelationData{
				Name:         rel.Name,
				SubjectUnion: buildSubjectUnion(rel.AllowedSubjects),
			}
			if subRefs[subRef{def.Name, rel.Name}] {
				rd.IsSubRef = true
				rd.SubRefTypeName = toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref"
			}
			dd.Relations = append(dd.Relations, rd)
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
// For caveated subjects, it returns the caveated variant type name.
func subjectRefTypeName(st schema.SubjectType) string {
	if st.CaveatName != "" {
		return caveatedSubjectTypeName(st)
	}
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

// toCamelCase converts a snake_case string to camelCase.
func toCamelCase(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		if i == 0 {
			result.WriteString(p)
		} else {
			result.WriteString(strings.ToUpper(p[:1]))
			result.WriteString(p[1:])
		}
	}
	return result.String()
}

// celTypeToTS converts a CEL type name to a TypeScript type.
func celTypeToTS(celType string) string {
	switch celType {
	case "string":
		return "string"
	case "int", "uint":
		return "number"
	case "double":
		return "number"
	case "bool":
		return "boolean"
	case "duration", "timestamp":
		return "string"
	default:
		return "unknown"
	}
}

// caveatedSubjectTypeName returns the TypeScript type name for a caveated subject type.
func caveatedSubjectTypeName(st schema.SubjectType) string {
	base := toPascalCase(st.Definition)
	if st.Relation != "" {
		base += toPascalCase(st.Relation)
	}
	return base + toPascalCase(st.CaveatName) + "Ref"
}
