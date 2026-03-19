package golang

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

// Generator implements generator.LanguageGenerator for Go.
type Generator struct{}

// Language returns "go".
func (g *Generator) Language() string {
	return "go"
}

// TemplateData holds all pre-computed data needed by the Go template.
type TemplateData struct {
	CaveatContextTypes []CaveatContextData
	SubjectRefTypes      []SubjectRefTypeData
	TypeSentinels        []TypeSentinelData
	Definitions          []DefinitionData
	SealedInterfaces     []SealedInterfaceData
	PermissionVars       []PermissionVarData
}

// CaveatContextData holds data for generating a caveat context struct.
type CaveatContextData struct {
	StructName string // e.g. "IPRangeContext"
	CaveatName string // e.g. "ip_range"
	Params     []CaveatParamData
}

// CaveatParamData holds data for a single caveat parameter.
type CaveatParamData struct {
	FieldName  string // PascalCase, e.g. "AllowedCidr"
	ParamName  string // original name, e.g. "allowed_cidr"
	GoType     string // Go type for the pointer field, e.g. "string"
	WithMethod string // e.g. "WithAllowedCidr"
}

// SubjectRefTypeData holds data for a subject ref type (base, caveated, or subref).
type SubjectRefTypeData struct {
	TypeName       string // e.g. "UserRef", "UserIPRange", "TeamMemberRef"
	DefinitionName string // e.g. "user", "team"
	SubRelation    string // e.g. "member" or ""
	CaveatName     string // e.g. "ip_range" or ""
	CaveatContext  string // e.g. "IPRangeContext" or ""
	HasCaveat      bool
	HasExpiration  bool
	// WithMethods are the caveat/expiration methods on base ref types
	WithMethods []WithMethodData
}

// WithMethodData holds data for a With* method on a base ref type.
type WithMethodData struct {
	MethodName    string // e.g. "WithIPRange"
	ContextType   string // e.g. "IPRangeContext"
	ReturnType    string // e.g. "UserIPRange"
	IsExpiration  bool
}

// TypeSentinelData holds data for a type sentinel (used for LookupSubjects).
type TypeSentinelData struct {
	VarName        string // e.g. "UserType"
	InternalType   string // e.g. "userRefType"
	DefinitionName string // e.g. "user"
	SubRelation    string // e.g. "" for base types
	MarkerMethods  []string
}

// DefinitionData holds pre-computed data for a single definition.
type DefinitionData struct {
	Name          string // original name, e.g. "document"
	PascalName    string // PascalCase name, e.g. "Document"
	HasSubjectRef bool   // true if {PascalName}Ref exists in SubjectRefTypes
	Relations     []RelationData
	Permissions   []PermissionData
	SubRefMethods []SubRefMethodData
}

// RelationData holds data for a relation on a definition.
type RelationData struct {
	Name          string // original name, e.g. "viewer"
	PascalName    string // e.g. "Viewer"
	InterfaceName string // e.g. "DocumentViewerSubject"
}

// PermissionData holds data for a permission on a definition.
type PermissionData struct {
	Name          string // e.g. "view"
	PascalName    string // e.g. "View"
	InterfaceName string // e.g. "DocumentViewSubject"
}

// SubRefMethodData holds data for a sub-ref accessor method on a definition ref.
type SubRefMethodData struct {
	MethodName string // e.g. "Member"
	ReturnType string // e.g. "TeamMemberRef"
}


// SealedInterfaceData holds data for generating a sealed interface.
type SealedInterfaceData struct {
	Name         string   // e.g. "DocumentViewerSubject"
	MarkerMethod string   // e.g. "isDocumentViewerSubject"
	Implementors []string // type names that implement this interface
}

// PermissionVarData holds data for a static permission ref var.
type PermissionVarData struct {
	VarName        string // e.g. "Document_View"
	InterfaceName  string // e.g. "DocumentViewSubject"
	ResourceType   string // e.g. "document"
	PermissionName string // e.g. "view"
}

// Generate produces generated Go files from the parsed schema.
func (g *Generator) Generate(s *schema.Schema) ([]generator.GeneratedFile, error) {
	data := buildTemplateData(s)

	funcMap := template.FuncMap{
		"hasRelations": func(d DefinitionData) bool {
			return len(d.Relations) > 0
		},
		"hasPermissions": func(d DefinitionData) bool {
			return len(d.Permissions) > 0
		},
		"hasRelationsOrPermissions": func(d DefinitionData) bool {
			return len(d.Relations) > 0 || len(d.Permissions) > 0
		},
		"hasSubRefMethods": func(d DefinitionData) bool {
			return len(d.SubRefMethods) > 0
		},
		"hasWithMethods": func(s SubjectRefTypeData) bool {
			return len(s.WithMethods) > 0
		},
		"toLowerCamel": toLowerCamel,
		"trimSuffix":   strings.TrimSuffix,
	}

	tmpl, err := template.New("typed_client.go.tmpl").Funcs(funcMap).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return []generator.GeneratedFile{
		{
			Path:    "permissions.gen.go",
			Content: buf.Bytes(),
		},
	}, nil
}

// buildTemplateData pre-computes all data needed by the Go template.
func buildTemplateData(s *schema.Schema) TemplateData {
	// Collect all (definition, relation) pairs referenced as subjects.
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

	// Collect used caveats (caveat name -> set of definition names that use them).
	// Also collect per-definition caveat usage: which caveats each definition can have.
	usedCaveats := map[string]bool{}
	// defCaveats maps definition name -> list of caveat names used with that definition as subject
	defCaveats := map[string][]string{}
	defCaveatsSeen := map[string]map[string]bool{}
	// defExpiration tracks whether a definition has "with expiration"
	defExpiration := map[string]bool{}

	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.CaveatName != "" {
					usedCaveats[st.CaveatName] = true
					if defCaveatsSeen[st.Definition] == nil {
						defCaveatsSeen[st.Definition] = map[string]bool{}
					}
					if !defCaveatsSeen[st.Definition][st.CaveatName] {
						defCaveatsSeen[st.Definition][st.CaveatName] = true
						defCaveats[st.Definition] = append(defCaveats[st.Definition], st.CaveatName)
					}
				}
				if st.Expiration {
					defExpiration[st.Definition] = true
				}
			}
		}
	}

	// Build CaveatContextTypes
	var caveatContextTypes []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		ccd := CaveatContextData{
			StructName: toPascalCase(c.Name) + "Context",
			CaveatName: c.Name,
		}
		for _, p := range c.Params {
			ccd.Params = append(ccd.Params, CaveatParamData{
				FieldName:  toPascalCase(p.Name),
				ParamName:  p.Name,
				GoType:     celTypeToGoType(p.Type),
				WithMethod: "With" + toPascalCase(p.Name),
			})
		}
		caveatContextTypes = append(caveatContextTypes, ccd)
	}

	// Build SubjectRefTypes: base refs, caveated variants, and sub-refs.
	// Track which definitions have a subject ref.
	hasSubjectRefMap := map[string]bool{}
	var subjectRefTypes []SubjectRefTypeData
	generatedTypes := map[string]bool{}

	// Determine which definitions are used as subjects (base type, no relation).
	defIsSubject := map[string]bool{}
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.Relation == "" && !st.Wildcard {
					defIsSubject[st.Definition] = true
				}
			}
		}
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				if st.Relation == "" && !st.Wildcard {
					defIsSubject[st.Definition] = true
				}
			}
		}
	}

	// Generate base ref types for definitions used as subjects
	for _, def := range s.Definitions {
		if !defIsSubject[def.Name] {
			continue
		}
		typeName := toPascalCase(def.Name) + "Ref"
		if generatedTypes[typeName] {
			continue
		}
		generatedTypes[typeName] = true
		hasSubjectRefMap[def.Name] = true

		srt := SubjectRefTypeData{
			TypeName:       typeName,
			DefinitionName: def.Name,
			HasExpiration:  defExpiration[def.Name],
		}

		// Add With* methods for each caveat this def can have
		for _, caveatName := range defCaveats[def.Name] {
			caveatedTypeName := toPascalCase(def.Name) + toPascalCase(caveatName)
			srt.WithMethods = append(srt.WithMethods, WithMethodData{
				MethodName:  "With" + toPascalCase(caveatName),
				ContextType: toPascalCase(caveatName) + "Context",
				ReturnType:  caveatedTypeName,
			})
		}

		// Add WithExpiration method if this def can have expiration
		if defExpiration[def.Name] {
			expirationTypeName := toPascalCase(def.Name) + "Expiring"
			srt.WithMethods = append(srt.WithMethods, WithMethodData{
				MethodName:   "WithExpiration",
				ReturnType:   expirationTypeName,
				IsExpiration: true,
			})
		}

		subjectRefTypes = append(subjectRefTypes, srt)

		// Generate caveated variants
		for _, caveatName := range defCaveats[def.Name] {
			caveatedTypeName := toPascalCase(def.Name) + toPascalCase(caveatName)
			if generatedTypes[caveatedTypeName] {
				continue
			}
			generatedTypes[caveatedTypeName] = true
			subjectRefTypes = append(subjectRefTypes, SubjectRefTypeData{
				TypeName:       caveatedTypeName,
				DefinitionName: def.Name,
				HasCaveat:      true,
				CaveatName:     caveatName,
				CaveatContext:  toPascalCase(caveatName) + "Context",
			})
		}

		// Generate expiration variant
		if defExpiration[def.Name] {
			expirationTypeName := toPascalCase(def.Name) + "Expiring"
			if !generatedTypes[expirationTypeName] {
				generatedTypes[expirationTypeName] = true
				subjectRefTypes = append(subjectRefTypes, SubjectRefTypeData{
					TypeName:       expirationTypeName,
					DefinitionName: def.Name,
					HasExpiration:  true,
				})
			}
		}
	}

	// Generate sub-ref types
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			sr := subRef{def.Name, rel.Name}
			if !subRefs[sr] {
				continue
			}
			typeName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref"
			if generatedTypes[typeName] {
				continue
			}
			generatedTypes[typeName] = true
			subjectRefTypes = append(subjectRefTypes, SubjectRefTypeData{
				TypeName:       typeName,
				DefinitionName: def.Name,
				SubRelation:    rel.Name,
			})
		}
	}

	// Build sealed interfaces - one per relation (allowed subjects) and one per permission (reachable subjects)
	var sealedInterfaces []SealedInterfaceData
	// Also build a map of type -> list of interfaces it implements, for type sentinels
	typeInterfaces := map[string][]string{}

	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			ifaceName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject"
			markerMethod := "is" + ifaceName
			implementors := collectImplementors(rel.AllowedSubjects, generatedTypes)
			if len(implementors) == 0 {
				continue
			}
			sealedInterfaces = append(sealedInterfaces, SealedInterfaceData{
				Name:         ifaceName,
				MarkerMethod: markerMethod,
				Implementors: implementors,
			})
			for _, impl := range implementors {
				typeInterfaces[impl] = append(typeInterfaces[impl], markerMethod)
			}
		}
		for _, perm := range def.Permissions {
			ifaceName := toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject"
			markerMethod := "is" + ifaceName
			implementors := collectImplementors(perm.ReachableSubjects, generatedTypes)
			if len(implementors) == 0 {
				continue
			}
			sealedInterfaces = append(sealedInterfaces, SealedInterfaceData{
				Name:         ifaceName,
				MarkerMethod: markerMethod,
				Implementors: implementors,
			})
			for _, impl := range implementors {
				typeInterfaces[impl] = append(typeInterfaces[impl], markerMethod)
			}
		}
	}

	// Build type sentinels for definitions used as base subjects
	var typeSentinels []TypeSentinelData
	for _, def := range s.Definitions {
		if !defIsSubject[def.Name] {
			continue
		}
		refTypeName := toPascalCase(def.Name) + "Ref"
		varName := toPascalCase(def.Name) + "Type"
		internalType := toLowerCamel(def.Name) + "RefType"
		ts := TypeSentinelData{
			VarName:        varName,
			InternalType:   internalType,
			DefinitionName: def.Name,
		}
		// Copy marker methods from the corresponding ref type
		if markers, ok := typeInterfaces[refTypeName]; ok {
			ts.MarkerMethods = markers
		}
		typeSentinels = append(typeSentinels, ts)
	}

	// Build type sentinels for sub-ref types too
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			sr := subRef{def.Name, rel.Name}
			if !subRefs[sr] {
				continue
			}
			refTypeName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref"
			varName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Type"
			internalType := toLowerCamel(def.Name) + toPascalCase(rel.Name) + "RefType"
			ts := TypeSentinelData{
				VarName:        varName,
				InternalType:   internalType,
				DefinitionName: def.Name,
				SubRelation:    rel.Name,
			}
			if markers, ok := typeInterfaces[refTypeName]; ok {
				ts.MarkerMethods = markers
			}
			typeSentinels = append(typeSentinels, ts)
		}
	}

	// Build DefinitionData
	var definitions []DefinitionData
	for _, def := range s.Definitions {
		dd := DefinitionData{
			Name:          def.Name,
			PascalName:    toPascalCase(def.Name),
			HasSubjectRef: hasSubjectRefMap[def.Name],
		}

		for _, rel := range def.Relations {
			dd.Relations = append(dd.Relations, RelationData{
				Name:          rel.Name,
				PascalName:    toPascalCase(rel.Name),
				InterfaceName: toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject",
			})
		}

		for _, perm := range def.Permissions {
			dd.Permissions = append(dd.Permissions, PermissionData{
				Name:          perm.Name,
				PascalName:    toPascalCase(perm.Name),
				InterfaceName: toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject",
			})
		}

		// Build sub-ref methods and detect conflicts with relation names.
		// When a sub-ref name conflicts with a relation name (e.g., team.member is
		// both a relation and a sub-ref), the sub-ref method always wins (no args,
		// returns the ref type), and the relation write method gets a "For" prefix
		// (e.g., ForMember).
		subRefNames := map[string]bool{}
		for _, rel := range def.Relations {
			sr := subRef{def.Name, rel.Name}
			if !subRefs[sr] {
				continue
			}
			methodName := toPascalCase(rel.Name)
			returnType := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref"
			subRefNames[methodName] = true
			dd.SubRefMethods = append(dd.SubRefMethods, SubRefMethodData{
				MethodName: methodName,
				ReturnType: returnType,
			})
		}
		// Rename conflicting relation methods with "For" prefix
		for i, rel := range dd.Relations {
			if subRefNames[rel.PascalName] {
				dd.Relations[i].PascalName = "For" + rel.PascalName
			}
		}

		definitions = append(definitions, dd)
	}

	// Build permission vars
	var permissionVars []PermissionVarData
	for _, def := range s.Definitions {
		for _, perm := range def.Permissions {
			permissionVars = append(permissionVars, PermissionVarData{
				VarName:        toPascalCase(def.Name) + "_" + toPascalCase(perm.Name),
				InterfaceName:  toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject",
				ResourceType:   def.Name,
				PermissionName: perm.Name,
			})
		}
	}

	return TemplateData{
		CaveatContextTypes: caveatContextTypes,
		SubjectRefTypes:    subjectRefTypes,
		TypeSentinels:      typeSentinels,
		Definitions:        definitions,
		SealedInterfaces:   sealedInterfaces,
		PermissionVars:     permissionVars,

	}
}

// collectImplementors returns the list of type names that should implement a sealed interface.
func collectImplementors(subjects []schema.SubjectType, knownTypes map[string]bool) []string {
	seen := map[string]bool{}
	var result []string
	for _, st := range subjects {
		name := goSubjectRefTypeName(st)
		if seen[name] || !knownTypes[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	// Sort for deterministic output
	sort.Strings(result)
	return result
}

// goSubjectRefTypeName returns the Go ref type name for a subject type.
func goSubjectRefTypeName(st schema.SubjectType) string {
	if st.Relation != "" {
		return toPascalCase(st.Definition) + toPascalCase(st.Relation) + "Ref"
	}
	if st.CaveatName != "" {
		return toPascalCase(st.Definition) + toPascalCase(st.CaveatName)
	}
	if st.Expiration {
		return toPascalCase(st.Definition) + "Expiring"
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

// toLowerCamel converts a snake_case string to lowerCamelCase.
func toLowerCamel(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		if i == 0 {
			result.WriteString(strings.ToLower(p[:1]))
			result.WriteString(p[1:])
		} else {
			result.WriteString(strings.ToUpper(p[:1]))
			result.WriteString(p[1:])
		}
	}
	return result.String()
}

// celTypeToGoType maps CEL type strings to Go type strings.
func celTypeToGoType(celType string) string {
	switch celType {
	case "string":
		return "string"
	case "int":
		return "int64"
	case "uint":
		return "uint64"
	case "double":
		return "float64"
	case "bool":
		return "bool"
	case "duration":
		return "string"
	case "timestamp":
		return "string"
	default:
		return "any"
	}
}
