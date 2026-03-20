package java

import (
	"bytes"
	"embed"
	"fmt"
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

// Generator implements generator.LanguageGenerator for Java.
type Generator struct{}

// Language returns "java".
func (g *Generator) Language() string {
	return "java"
}

// Generate produces generated Java files from the parsed schema.
// opts must contain "package" specifying the Java package name.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	pkg, ok := opts["package"]
	if !ok || pkg == "" {
		return nil, fmt.Errorf("java generator requires --java.package=<package> option")
	}

	data := buildTemplateData(s, pkg)

	funcMap := template.FuncMap{
		"joinComma": func(ss []string) string {
			return strings.Join(ss, ", ")
		},
		"hasRelations": func(d DefinitionData) bool {
			return len(d.Relations) > 0
		},
		"hasPermissions": func(d DefinitionData) bool {
			return len(d.Permissions) > 0
		},
		"hasSubRefMethods": func(d DefinitionData) bool {
			return len(d.SubRefMethods) > 0
		},
		"hasWithMethods": func(srt SubjectRefTypeData) bool {
			return len(srt.WithMethods) > 0
		},
		"hasRelationsOrPermissions": func(d DefinitionData) bool {
			return len(d.Relations) > 0 || len(d.Permissions) > 0
		},
	}

	tmpl, err := template.New("typed_client.java.tmpl").Funcs(funcMap).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	return []generator.GeneratedFile{
		{
			Path:    "Permissions.java",
			Content: buf.Bytes(),
		},
	}, nil
}

// TemplateData holds all pre-computed data needed by the Java template.
type TemplateData struct {
	Package            string
	CaveatContextTypes []CaveatContextData
	SubjectRefTypes    []SubjectRefTypeData
	TypeSentinels      []TypeSentinelData
	Definitions        []DefinitionData
	SealedInterfaces   []SealedInterfaceData
	PermissionVars     []PermissionVarData
}

// CaveatContextData holds data for generating a caveat context record.
type CaveatContextData struct {
	ClassName  string
	CaveatName string
	Params     []CaveatParamData
}

// CaveatParamData holds data for a single caveat parameter.
type CaveatParamData struct {
	FieldName  string
	ParamName  string
	JavaType   string
	WithMethod string
}

// SubjectRefTypeData holds data for a subject ref type.
type SubjectRefTypeData struct {
	TypeName       string
	DefinitionName string
	SubRelation    string
	CaveatName     string
	CaveatContext  string
	HasCaveat      bool
	HasExpiration  bool
	Interfaces     []string
	WithMethods    []WithMethodData
}

// WithMethodData holds data for a with* method on a base ref type.
type WithMethodData struct {
	MethodName  string
	ContextType string
	ReturnType  string
}

// TypeSentinelData holds data for a type sentinel.
type TypeSentinelData struct {
	ClassName      string
	VarName        string
	DefinitionName string
	SubRelation    string
	Interfaces     []string
}

// DefinitionData holds pre-computed data for a single definition.
type DefinitionData struct {
	Name          string
	PascalName    string
	IsSubject     bool
	Relations     []RelationData
	Permissions   []PermissionData
	SubRefMethods []SubRefMethodData
}

// RelationData holds data for a relation on a definition.
type RelationData struct {
	Name          string
	MethodName    string
	InterfaceName string
}

// PermissionData holds data for a permission on a definition.
type PermissionData struct {
	Name          string
	MethodName    string
	InterfaceName string
}

// SubRefMethodData holds data for a sub-ref accessor method on a definition ref.
type SubRefMethodData struct {
	MethodName string
	ReturnType string
}

// SealedInterfaceData holds data for generating a sealed interface.
type SealedInterfaceData struct {
	Name    string
	Permits []string
}

// PermissionVarData holds data for a static permission ref var.
type PermissionVarData struct {
	VarName        string
	InterfaceName  string
	ResourceType   string
	PermissionName string
}

// buildTemplateData pre-computes all data needed by the Java template.
func buildTemplateData(s *schema.Schema, pkg string) TemplateData {
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

	// Collect used caveats and per-definition caveat usage.
	usedCaveats := map[string]bool{}
	defCaveats := map[string][]string{}
	defCaveatsSeen := map[string]map[string]bool{}
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

	// Build CaveatContextTypes.
	var caveatContextTypes []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		ccd := CaveatContextData{
			ClassName:  toPascalCase(c.Name) + "Context",
			CaveatName: c.Name,
		}
		for _, p := range c.Params {
			ccd.Params = append(ccd.Params, CaveatParamData{
				FieldName:  toLowerCamel(p.Name),
				ParamName:  p.Name,
				JavaType:   celTypeToJavaType(p.Type),
				WithMethod: "with" + toPascalCase(p.Name),
			})
		}
		caveatContextTypes = append(caveatContextTypes, ccd)
	}

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

	// Build SubjectRefTypes.
	hasSubjectRefMap := map[string]bool{}
	var subjectRefTypes []SubjectRefTypeData
	generatedTypes := map[string]bool{}

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

		for _, caveatName := range defCaveats[def.Name] {
			caveatedTypeName := toPascalCase(def.Name) + toPascalCase(caveatName)
			srt.WithMethods = append(srt.WithMethods, WithMethodData{
				MethodName:  "with" + toPascalCase(caveatName),
				ContextType: toPascalCase(caveatName) + "Context",
				ReturnType:  caveatedTypeName,
			})
		}

		if defExpiration[def.Name] {
			expirationTypeName := toPascalCase(def.Name) + "Expiring"
			srt.WithMethods = append(srt.WithMethods, WithMethodData{
				MethodName: "withExpiration",
				ReturnType: expirationTypeName,
			})
		}

		subjectRefTypes = append(subjectRefTypes, srt)

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

	// Generate sub-ref types.
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

	// Build sealed interfaces and track type -> interfaces mapping.
	var sealedInterfaces []SealedInterfaceData
	typeInterfaces := map[string][]string{}

	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			ifaceName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject"
			implementors := collectImplementors(rel.AllowedSubjects, generatedTypes)
			sentinels := collectSentinelTypes(rel.AllowedSubjects)
			permits := dedup(sorted(append(implementors, sentinels...)))
			if len(permits) == 0 {
				continue
			}
			sealedInterfaces = append(sealedInterfaces, SealedInterfaceData{
				Name:    ifaceName,
				Permits: permits,
			})
			for _, impl := range implementors {
				typeInterfaces[impl] = appendUnique(typeInterfaces[impl], ifaceName)
			}
			for _, sent := range sentinels {
				typeInterfaces[sent] = appendUnique(typeInterfaces[sent], ifaceName)
			}
		}
		for _, perm := range def.Permissions {
			ifaceName := toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject"
			implementors := collectImplementors(perm.ReachableSubjects, generatedTypes)
			sentinels := collectSentinelTypes(perm.ReachableSubjects)
			permits := dedup(sorted(append(implementors, sentinels...)))
			if len(permits) == 0 {
				continue
			}
			sealedInterfaces = append(sealedInterfaces, SealedInterfaceData{
				Name:    ifaceName,
				Permits: permits,
			})
			for _, impl := range implementors {
				typeInterfaces[impl] = appendUnique(typeInterfaces[impl], ifaceName)
			}
			for _, sent := range sentinels {
				typeInterfaces[sent] = appendUnique(typeInterfaces[sent], ifaceName)
			}
		}
	}

	// Build top-level Subject sealed interface containing all sub-interfaces, concrete types, and sentinels.
	var subjectPermits []string
	for _, si := range sealedInterfaces {
		subjectPermits = append(subjectPermits, si.Name)
	}
	for _, srt := range subjectRefTypes {
		subjectPermits = append(subjectPermits, srt.TypeName)
	}
	// Add sentinel type names (computed here since sentinels are built later)
	for _, def := range s.Definitions {
		if defIsSubject[def.Name] {
			subjectPermits = append(subjectPermits, toPascalCase(def.Name)+"Type")
		}
		for _, rel := range def.Relations {
			if subRefs[subRef{def.Name, rel.Name}] {
				subjectPermits = append(subjectPermits, toPascalCase(def.Name)+toPascalCase(rel.Name)+"Type")
			}
		}
	}
	subjectPermits = dedup(sorted(subjectPermits))
	if len(subjectPermits) > 0 {
		sealedInterfaces = append([]SealedInterfaceData{{
			Name:    "Subject",
			Permits: subjectPermits,
		}}, sealedInterfaces...)
	}

	// Assign Interfaces to each SubjectRefType.
	for i := range subjectRefTypes {
		if ifaces, ok := typeInterfaces[subjectRefTypes[i].TypeName]; ok {
			subjectRefTypes[i].Interfaces = ifaces
		}
	}

	// Build type sentinels for definitions used as base subjects.
	var typeSentinels []TypeSentinelData
	for _, def := range s.Definitions {
		if !defIsSubject[def.Name] {
			continue
		}
		varName := toPascalCase(def.Name) + "Type"
		className := toPascalCase(def.Name) + "Type"
		ts := TypeSentinelData{
			ClassName:      className,
			VarName:        varName,
			DefinitionName: def.Name,
		}
		if ifaces, ok := typeInterfaces[varName]; ok {
			ts.Interfaces = ifaces
		}
		typeSentinels = append(typeSentinels, ts)
	}

	// Build type sentinels for sub-ref types.
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			sr := subRef{def.Name, rel.Name}
			if !subRefs[sr] {
				continue
			}
			varName := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Type"
			className := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Type"
			ts := TypeSentinelData{
				ClassName:      className,
				VarName:        varName,
				DefinitionName: def.Name,
				SubRelation:    rel.Name,
			}
			if ifaces, ok := typeInterfaces[varName]; ok {
				ts.Interfaces = ifaces
			}
			typeSentinels = append(typeSentinels, ts)
		}
	}

	// Build DefinitionData.
	var definitions []DefinitionData
	for _, def := range s.Definitions {
		dd := DefinitionData{
			Name:       def.Name,
			PascalName: toPascalCase(def.Name),
			IsSubject:  hasSubjectRefMap[def.Name],
		}

		for _, rel := range def.Relations {
			dd.Relations = append(dd.Relations, RelationData{
				Name:          rel.Name,
				MethodName:    toLowerCamel(rel.Name),
				InterfaceName: toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject",
			})
		}

		for _, perm := range def.Permissions {
			dd.Permissions = append(dd.Permissions, PermissionData{
				Name:          perm.Name,
				MethodName:    toLowerCamel(perm.Name),
				InterfaceName: toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject",
			})
		}

		// Build sub-ref methods and detect conflicts.
		subRefNames := map[string]bool{}
		for _, rel := range def.Relations {
			sr := subRef{def.Name, rel.Name}
			if !subRefs[sr] {
				continue
			}
			methodName := toLowerCamel(rel.Name)
			returnType := toPascalCase(def.Name) + toPascalCase(rel.Name) + "Ref"
			subRefNames[methodName] = true
			dd.SubRefMethods = append(dd.SubRefMethods, SubRefMethodData{
				MethodName: methodName,
				ReturnType: returnType,
			})
		}
		// Rename conflicting relation methods with "for" prefix.
		for i, rel := range dd.Relations {
			if subRefNames[rel.MethodName] {
				dd.Relations[i].MethodName = "for" + toPascalCase(rel.Name)
			}
		}

		definitions = append(definitions, dd)
	}

	// Build permission vars.
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
		Package:            pkg,
		CaveatContextTypes: caveatContextTypes,
		SubjectRefTypes:    subjectRefTypes,
		TypeSentinels:      typeSentinels,
		Definitions:        definitions,
		SealedInterfaces:   sealedInterfaces,
		PermissionVars:     permissionVars,
	}
}

// collectImplementors returns the list of type names from subjects that exist in knownTypes.
func collectImplementors(subjects []schema.SubjectType, knownTypes map[string]bool) []string {
	seen := map[string]bool{}
	var result []string
	for _, st := range subjects {
		name := javaSubjectRefTypeName(st)
		if seen[name] || !knownTypes[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// collectSentinelTypes returns the sentinel type names for subjects.
func collectSentinelTypes(subjects []schema.SubjectType) []string {
	seen := map[string]bool{}
	var result []string
	for _, st := range subjects {
		if st.Wildcard {
			continue
		}
		var name string
		if st.Relation != "" {
			name = toPascalCase(st.Definition) + toPascalCase(st.Relation) + "Type"
		} else {
			name = toPascalCase(st.Definition) + "Type"
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// javaSubjectRefTypeName returns the Java ref type name for a subject type.
func javaSubjectRefTypeName(st schema.SubjectType) string {
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

// dedup removes adjacent duplicates from a sorted slice.
func dedup(s []string) []string {
	if len(s) == 0 {
		return s
	}
	result := []string{s[0]}
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1] {
			result = append(result, s[i])
		}
	}
	return result
}

// sorted returns a sorted copy of the slice.
func sorted(s []string) []string {
	result := make([]string, len(s))
	copy(result, s)
	sort.Strings(result)
	return result
}

// appendUnique appends val to s only if it's not already present.
func appendUnique(s []string, val string) []string {
	for _, v := range s {
		if v == val {
			return s
		}
	}
	return append(s, val)
}

// toPascalCase converts a snake_case string to PascalCase.
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

// celTypeToJavaType maps CEL type strings to Java type strings.
func celTypeToJavaType(celType string) string {
	switch celType {
	case "string":
		return "String"
	case "int", "uint":
		return "Long"
	case "double":
		return "Double"
	case "bool":
		return "Boolean"
	case "duration", "timestamp":
		return "String"
	default:
		return "Object"
	}
}
