package python

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

// Generator implements generator.LanguageGenerator for Python.
type Generator struct{}

// Language returns "python".
func (g *Generator) Language() string { return "python" }

// TemplateData holds all pre-computed data needed by the Python template.
type TemplateData struct {
	CaveatContexts     []CaveatContextData
	SubjectRefs        []SubjectRefData
	Definitions        []DefinitionData
	PerPermissionTypes []PerPermTypeData
	SealedUnions       []SealedUnionData
	StaticPermRefs     []StaticPermRefData
	LookupSubjectTypes []LookupSubjectTypeData
	CheckOverloads     []CheckOverloadData
	LookupResOverloads []LookupResOverloadData
}

type LookupSubjectTypeData struct {
	PermClassName string   // e.g. "_DocumentViewPermission"
	Members       []string // e.g. ["TeamMember", "User", "UserIpRange", "UserTimeWindow"]
}

type CheckOverloadData struct {
	PermClassName string // e.g. "_DocumentViewPermission"
	SubjectUnion  string // e.g. "DocumentViewSubject"
}

type LookupResOverloadData struct {
	RefClassName string // e.g. "_DocumentViewRef"
	SubjectUnion string // e.g. "DocumentViewSubject"
}

type DefinitionData struct {
	ClassName   string             // e.g. "Document"
	Name        string             // e.g. "document"
	Permissions []DefPermissionData
	Relations   []DefRelationData
	SubRefs     []DefSubRefData
}

type DefPermissionData struct {
	PropName       string // snake_case method name, e.g. "view" or "del_" if escaped
	ReturnType     string // narrowed Permission subclass, e.g. "_DocumentViewPermission"
	PermissionName string // raw schema name, e.g. "view" — emitted verbatim into the body
}

type PerPermTypeData struct {
	PermClassName string // e.g. "_DocumentViewPermission"
	RefClassName  string // e.g. "_DocumentViewRef"
}

type SealedUnionData struct {
	AliasName string   // e.g. "DocumentViewerSubject"
	Members   []string // e.g. ["TeamMember", "User", "UserIpRange", "UserTimeWindow"]
}

type StaticPermRefData struct {
	ConstName    string // e.g. "DocumentView"
	RefClassName string // e.g. "_DocumentViewRef"
	ResourceType string // raw, e.g. "document"
	Permission   string // raw, e.g. "view"
}

type DefRelationData struct {
	MethodName   string // snake_case, possibly prefixed "for_"
	SubjectUnion string // e.g. "DocumentViewerSubject"
	RelationName string // raw schema name, used in body string
}

type DefSubRefData struct {
	PropName   string // snake_case
	ReturnType string // e.g. "TeamMember"
}

type CaveatContextData struct {
	ClassName  string         // e.g. "IpRangeContext"
	CaveatName string         // e.g. "ip_range"
	Params     []CaveatParamData
}

type CaveatParamData struct {
	FieldName string // Python identifier, e.g. "allowed_cidr" (escaped from keyword if needed)
	RawName   string // raw schema name, e.g. "id" — used as the CEL context dict key
	PyType    string // e.g. "str"
}

// SubjectRefData covers base refs ("User"), caveated variants ("UserIpRange"),
// expiring variants ("UserExpiring"), and sub-relation refs ("TeamMember").
type SubjectRefData struct {
	ClassName      string // e.g. "User", "UserIpRange", "TeamMember"
	DefinitionName string // e.g. "user"
	SubRelation    string // e.g. "member" — empty unless this is a sub-ref
	CaveatName     string // e.g. "ip_range" — empty unless caveated
	CaveatContext  string // e.g. "IpRangeContext"
	HasCaveat      bool
	HasExpiration  bool   // true for an expiring variant
	WithMethods    []WithMethodData
}

type WithMethodData struct {
	MethodName   string // e.g. "with_ip_range" or "with_expiration"
	ParamType    string // e.g. "IpRangeContext" or "datetime"
	ReturnType   string // e.g. "UserIpRange" or "UserExpiring"
	IsExpiration bool
	IsCaveat     bool
	CaveatName   string // only set when IsCaveat — used by emitter if needed
}

// Generate produces a permissions.py file from the parsed schema.
// opts is currently unused.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	data, err := buildTemplateData(s)
	if err != nil {
		return nil, err
	}

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

func buildTemplateData(s *schema.Schema) (TemplateData, error) {
	usedCaveats := map[string]bool{}
	defCaveats := map[string][]string{}         // def name -> ordered caveat names usable on it
	defCaveatsSeen := map[string]map[string]bool{}
	defExpiration := map[string]bool{}           // def name -> uses "with expiration"
	defIsSubject := map[string]bool{}            // def name -> appears as a base subject anywhere
	type subRef struct{ def, rel string }
	subRefs := map[subRef]bool{}

	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				} else if !st.Wildcard {
					defIsSubject[st.Definition] = true
				}
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
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				if st.Relation != "" {
					subRefs[subRef{st.Definition, st.Relation}] = true
				} else if !st.Wildcard {
					defIsSubject[st.Definition] = true
				}
			}
		}
	}

	// Caveat contexts (preserves the Task 4 behavior, including RawName).
	var caveatContexts []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		cc := CaveatContextData{
			ClassName:  toPascalCase(c.Name) + "Context",
			CaveatName: c.Name,
		}
		params := append([]schema.CaveatParam(nil), c.Params...)
		sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
		for _, p := range params {
			cc.Params = append(cc.Params, CaveatParamData{
				FieldName: escapeKeyword(p.Name),
				RawName:   p.Name,
				PyType:    celTypeToPyType(p.Type),
			})
		}
		caveatContexts = append(caveatContexts, cc)
	}

	// Subject refs.
	generated := map[string]bool{}
	var subjectRefs []SubjectRefData

	for _, def := range s.Definitions {
		if !defIsSubject[def.Name] {
			continue
		}
		base := toPascalCase(def.Name)
		if generated[base] {
			continue
		}
		generated[base] = true

		baseRef := SubjectRefData{
			ClassName:      base,
			DefinitionName: def.Name,
		}
		for _, caveatName := range defCaveats[def.Name] {
			variant := base + toPascalCase(caveatName)
			baseRef.WithMethods = append(baseRef.WithMethods, WithMethodData{
				MethodName: "with_" + caveatName,
				ParamType:  toPascalCase(caveatName) + "Context",
				ReturnType: variant,
				IsCaveat:   true,
				CaveatName: caveatName,
			})
		}
		if defExpiration[def.Name] {
			baseRef.WithMethods = append(baseRef.WithMethods, WithMethodData{
				MethodName:   "with_expiration",
				ParamType:    "datetime",
				ReturnType:   base + "Expiring",
				IsExpiration: true,
			})
		}
		subjectRefs = append(subjectRefs, baseRef)

		for _, caveatName := range defCaveats[def.Name] {
			variant := base + toPascalCase(caveatName)
			if generated[variant] {
				continue
			}
			generated[variant] = true
			subjectRefs = append(subjectRefs, SubjectRefData{
				ClassName:      variant,
				DefinitionName: def.Name,
				HasCaveat:      true,
				CaveatName:     caveatName,
				CaveatContext:  toPascalCase(caveatName) + "Context",
			})
		}

		if defExpiration[def.Name] {
			variant := base + "Expiring"
			if !generated[variant] {
				generated[variant] = true
				subjectRefs = append(subjectRefs, SubjectRefData{
					ClassName:      variant,
					DefinitionName: def.Name,
					HasExpiration:  true,
				})
			}
		}
	}

	// Sub-relation refs.
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			if !subRefs[subRef{def.Name, rel.Name}] {
				continue
			}
			name := toPascalCase(def.Name) + toPascalCase(rel.Name)
			if generated[name] {
				continue
			}
			generated[name] = true
			subjectRefs = append(subjectRefs, SubjectRefData{
				ClassName:      name,
				DefinitionName: def.Name,
				SubRelation:    rel.Name,
			})
		}
	}

	// Sealed subject TypeAlias unions.
	collectMembers := func(subjects []schema.SubjectType) []string {
		seen := map[string]bool{}
		var out []string
		for _, st := range subjects {
			name := pySubjectRefName(st)
			if !generated[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}

	var sealedUnions []SealedUnionData
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			members := collectMembers(rel.AllowedSubjects)
			if len(members) == 0 {
				continue
			}
			sealedUnions = append(sealedUnions, SealedUnionData{
				AliasName: toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject",
				Members:   members,
			})
		}
		for _, perm := range def.Permissions {
			members := collectMembers(perm.ReachableSubjects)
			if len(members) == 0 {
				continue
			}
			sealedUnions = append(sealedUnions, SealedUnionData{
				AliasName: toPascalCase(def.Name) + toPascalCase(perm.Name) + "Subject",
				Members:   members,
			})
		}
	}

	// Definitions (resource side).
	var definitions []DefinitionData
	for _, def := range s.Definitions {
		dd := DefinitionData{
			ClassName: toPascalCase(def.Name),
			Name:      def.Name,
		}

		// Sub-refs as @property accessors. Track names to detect collisions with relation names.
		subRefNames := map[string]bool{}
		for _, rel := range def.Relations {
			if !subRefs[subRef{def.Name, rel.Name}] {
				continue
			}
			propName := escapeKeyword(toSnakeCase(rel.Name))
			subRefNames[rel.Name] = true
			dd.SubRefs = append(dd.SubRefs, DefSubRefData{
				PropName:   propName,
				ReturnType: toPascalCase(def.Name) + toPascalCase(rel.Name),
			})
		}

		// Permissions as @property accessors returning the narrowed Permission subclass.
		for _, perm := range def.Permissions {
			dd.Permissions = append(dd.Permissions, DefPermissionData{
				PropName:       escapeKeyword(toSnakeCase(perm.Name)),
				ReturnType:     "_" + toPascalCase(def.Name) + toPascalCase(perm.Name) + "Permission",
				PermissionName: perm.Name,
			})
		}

		// Relation write methods. If the relation name collides with a sub-ref, prefix "for_".
		for _, rel := range def.Relations {
			methodName := escapeKeyword(toSnakeCase(rel.Name))
			if subRefNames[rel.Name] {
				methodName = "for_" + methodName
			}
			dd.Relations = append(dd.Relations, DefRelationData{
				MethodName:   methodName,
				SubjectUnion: toPascalCase(def.Name) + toPascalCase(rel.Name) + "Subject",
				RelationName: rel.Name,
			})
		}

		// If a definition is BOTH used as a subject AND has resource-side accessors
		// (permissions, relations, or sub-refs), naively emitting both classes would
		// produce a duplicate `class X:` declaration — Python lets the second one
		// silently win, destroying the subject-ref's caveat builders and protocol
		// methods. Detect and reject; merging the two declarations into one is the
		// proper fix and will land in a follow-up.
		if defIsSubject[def.Name] && (len(dd.Permissions) > 0 || len(dd.Relations) > 0 || len(dd.SubRefs) > 0) {
			return TemplateData{}, fmt.Errorf(
				"definition %q is referenced as both a subject and as a resource with its own "+
					"permissions/relations/sub-refs; the python generator does not yet support "+
					"merging these into a single class. Split the schema so this definition is "+
					"used only as a subject or only as a resource, or open an issue", def.Name)
		}

		// Skip the empty-resource-side case (def is only a subject, has no resource-side
		// members to add): the subject-ref class already covers it.
		if defIsSubject[def.Name] {
			continue
		}

		definitions = append(definitions, dd)
	}

	// Per-permission narrowing subclasses.
	var perPermTypes []PerPermTypeData
	for _, def := range s.Definitions {
		for _, perm := range def.Permissions {
			base := toPascalCase(def.Name) + toPascalCase(perm.Name)
			perPermTypes = append(perPermTypes, PerPermTypeData{
				PermClassName: "_" + base + "Permission",
				RefClassName:  "_" + base + "Ref",
			})
		}
	}

	// Static permission ref constants.
	var staticPermRefs []StaticPermRefData
	for _, def := range s.Definitions {
		for _, perm := range def.Permissions {
			base := toPascalCase(def.Name) + toPascalCase(perm.Name)
			staticPermRefs = append(staticPermRefs, StaticPermRefData{
				ConstName:    base,
				RefClassName: "_" + base + "Ref",
				ResourceType: def.Name,
				Permission:   perm.Name,
			})
		}
	}

	var checkOverloads []CheckOverloadData
	var lookupResOverloads []LookupResOverloadData
	var lookupSubjectTypes []LookupSubjectTypeData
	for _, def := range s.Definitions {
		for _, perm := range def.Permissions {
			base := toPascalCase(def.Name) + toPascalCase(perm.Name)
			checkOverloads = append(checkOverloads, CheckOverloadData{
				PermClassName: "_" + base + "Permission",
				SubjectUnion:  base + "Subject",
			})
			lookupResOverloads = append(lookupResOverloads, LookupResOverloadData{
				RefClassName: "_" + base + "Ref",
				SubjectUnion: base + "Subject",
			})

			// Collect the subject-type member set for this permission.
			members := collectMembers(perm.ReachableSubjects)
			if len(members) == 0 {
				continue
			}
			lookupSubjectTypes = append(lookupSubjectTypes, LookupSubjectTypeData{
				PermClassName: "_" + base + "Permission",
				Members:       members,
			})
		}
	}

	return TemplateData{
		CaveatContexts:     caveatContexts,
		SubjectRefs:        subjectRefs,
		Definitions:        definitions,
		PerPermissionTypes: perPermTypes,
		SealedUnions:       sealedUnions,
		StaticPermRefs:     staticPermRefs,
		LookupSubjectTypes: lookupSubjectTypes,
		CheckOverloads:     checkOverloads,
		LookupResOverloads: lookupResOverloads,
	}, nil
}

// pySubjectRefName returns the Python class name for a given subject type.
// Used by sealed-union and lookup-sentinel emitters to refer to refs by name.
// Mirrors goSubjectRefTypeName in spicedb-gen/golang/generator.go.
func pySubjectRefName(st schema.SubjectType) string {
	if st.Relation != "" {
		return toPascalCase(st.Definition) + toPascalCase(st.Relation)
	}
	if st.CaveatName != "" {
		return toPascalCase(st.Definition) + toPascalCase(st.CaveatName)
	}
	if st.Expiration {
		return toPascalCase(st.Definition) + "Expiring"
	}
	return toPascalCase(st.Definition)
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
