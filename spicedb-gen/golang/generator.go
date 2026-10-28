package golang

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"go/token"
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

// DefaultPackage is the generated package name when --go.package is not set.
const DefaultPackage = "permissions"

// TemplateData holds all pre-computed data needed by the Go template.
type TemplateData struct {
	Package            string
	CaveatContextTypes []CaveatContextData
	SubjectRefTypes    []SubjectRefTypeData
	TypeSentinels      []TypeSentinelData
	Definitions        []DefinitionData
	SealedInterfaces   []SealedInterfaceData
	PermissionVars     []PermissionVarData
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

// SubjectRefTypeData holds data for one subject ref type: a base ref (direct,
// userset or wildcard) or one of its caveated/expiring variants.
type SubjectRefTypeData struct {
	TypeName       string // e.g. "UserRef", "UserIpRange", "TeamMemberRef", "UserWildcardRef"
	DefinitionName string // e.g. "user", "team"
	SubRelation    string // e.g. "member" or ""
	Wildcard       bool   // subject ID is always "*"
	CaveatName     string // e.g. "ip_range" or ""
	CaveatContext  string // e.g. "IpRangeContext" or ""
	HasCaveat      bool
	HasExpiration  bool
	// IsDefinitionRef marks the direct base ref of a definition (e.g. UserRef),
	// which is declared alongside the definition's resource methods.
	IsDefinitionRef bool
	// WithMethods convert this ref to a caveated or expiring variant.
	WithMethods []WithMethodData
}

// WithMethodData holds data for a With* method on a subject ref type.
type WithMethodData struct {
	MethodName   string // e.g. "WithIpRange"
	ContextType  string // e.g. "IpRangeContext"
	ReturnType   string // e.g. "UserIpRange"
	IsExpiration bool
	// CarryContext copies a caveat context into the returned variant.
	CarryContext bool
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
	Name           string // original name, e.g. "document"
	PascalName     string // PascalCase name, e.g. "Document"
	HasSubjectRef  bool   // true if {PascalName}Ref is also a subject ref
	WildcardType   string // e.g. "UserWildcardRef" when user:* is allowed somewhere
	Relations      []RelationData
	Permissions    []PermissionData
	SubRefMethods  []SubRefMethodData
	RelationNames  []NameData
	PermissionKeys []NameData
}

// NameData pairs a schema name with its Go field name.
type NameData struct {
	Name       string
	PascalName string
}

// RelationData holds data for a relation on a definition.
type RelationData struct {
	Name             string // original name, e.g. "viewer"
	PascalName       string // writer method name, e.g. "Viewer" or "ForMember"
	InterfaceName    string // e.g. "DocumentViewerSubject"
	KeyMethod        string // e.g. "ViewerKey"
	KeyInterface     string // e.g. "DocumentViewerKeySubject"
	CheckMethod      string // e.g. "ViewerRelation"
	FilterMethod     string // e.g. "ViewerFilter"
	HasSubjects      bool   // false when no subject type could be generated
	HasKeyInterface  bool
	AllowedSubjectsT string // human-readable allowed subjects, for docs
}

// PermissionData holds data for a permission on a definition.
type PermissionData struct {
	Name          string // e.g. "view"
	PascalName    string // e.g. "View"
	InterfaceName string // e.g. "DocumentViewSubject"
}

// SubRefMethodData holds data for a sub-ref accessor method on a definition ref.
type SubRefMethodData struct {
	MethodName string // e.g. "Member" or "CanAdminSubject"
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

// Generate produces generated Go files from the parsed schema. The only
// language option is "package" (--go.package), the generated package name.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	pkg := DefaultPackage
	for k, v := range opts {
		switch k {
		case "package":
			if !token.IsIdentifier(v) || token.IsKeyword(v) {
				return nil, fmt.Errorf("--go.package=%q is not a valid Go package name", v)
			}
			pkg = v
		default:
			return nil, fmt.Errorf("unknown Go option --go.%s", k)
		}
	}

	data, err := buildTemplateData(s)
	if err != nil {
		return nil, err
	}
	data.Package = pkg

	funcMap := template.FuncMap{
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
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("generated Go does not parse (generator bug): %w", err)
	}

	return []generator.GeneratedFile{
		{
			Path:    "permissions.gen.go",
			Content: formatted,
		},
	}, nil
}

// subjectBase identifies a subject position independent of caveat and
// expiration: a direct object, a userset (object#relation, where relation may
// be a relation or a permission) or a wildcard (type:*).
type subjectBase struct {
	definition string
	relation   string
	wildcard   bool
}

func baseOf(st schema.SubjectType) subjectBase {
	return subjectBase{st.Definition, st.Relation, st.Wildcard}
}

// prefix is the type-name stem shared by a base and its variants.
func (b subjectBase) prefix() string {
	switch {
	case b.wildcard:
		return toPascalCase(b.definition) + "Wildcard"
	case b.relation != "":
		return toPascalCase(b.definition) + toPascalCase(b.relation)
	default:
		return toPascalCase(b.definition)
	}
}

func (b subjectBase) refType() string { return b.prefix() + "Ref" }

// goSubjectRefTypeName returns the Go ref type name for a subject type.
func goSubjectRefTypeName(st schema.SubjectType) string {
	return variantTypeName(baseOf(st), st.CaveatName, st.Expiration)
}

func variantTypeName(b subjectBase, caveat string, expiration bool) string {
	if caveat == "" && !expiration {
		return b.refType()
	}
	name := b.prefix() + toPascalCase(caveat)
	if expiration {
		name += "Expiring"
	}
	return name
}

type variantKey struct {
	base       subjectBase
	caveat     string
	expiration bool
}

// names detects collisions among generated identifiers. Package-level names
// share one scope; each definition ref has its own method scope.
type names struct {
	owners map[string]string
	err    error
}

func newNames() *names { return &names{owners: map[string]string{}} }

func (n *names) claim(scope, name, owner string) {
	key := scope + "." + name
	if prev, ok := n.owners[key]; ok && prev != owner && n.err == nil {
		where := "package scope"
		if scope != "" {
			where = scope
		}
		n.err = fmt.Errorf("schema produces conflicting Go identifier %s in %s (from %s and %s); rename one of them", name, where, prev, owner)
	}
	n.owners[key] = owner
}

// buildTemplateData pre-computes all data needed by the Go template.
func buildTemplateData(s *schema.Schema) (TemplateData, error) {
	// Every subject variant a relation admits, plus every subject position a
	// permission can reach. Permission-reachable usersets need refs too: a
	// relation may admit org#can_admin even though can_admin is a permission.
	variants := map[variantKey]bool{}
	bases := map[subjectBase]bool{}
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				variants[variantKey{baseOf(st), st.CaveatName, st.Expiration}] = true
				bases[baseOf(st)] = true
			}
		}
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				bases[baseOf(st)] = true
			}
		}
	}
	for b := range bases {
		variants[variantKey{base: b}] = true
	}
	// A caveated, expiring subject is built as Base.WithCaveat(ctx).WithExpiration(t),
	// so its caveated variant must exist as the intermediate step.
	for v := range variants {
		if v.caveat != "" && v.expiration {
			variants[variantKey{v.base, v.caveat, false}] = true
		}
	}

	usedCaveats := map[string]bool{}
	for v := range variants {
		if v.caveat != "" {
			usedCaveats[v.caveat] = true
		}
	}

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

	n := newNames()
	defNames := map[string]bool{}
	for _, def := range s.Definitions {
		defNames[def.Name] = true
	}

	// Subject ref types, in schema order: for each definition, its direct
	// base, then usersets on its relations and permissions, then wildcard,
	// each followed by its variants.
	var orderedBases []subjectBase
	for _, def := range s.Definitions {
		candidates := []subjectBase{{definition: def.Name}}
		for _, rel := range def.Relations {
			candidates = append(candidates, subjectBase{definition: def.Name, relation: rel.Name})
		}
		for _, perm := range def.Permissions {
			candidates = append(candidates, subjectBase{definition: def.Name, relation: perm.Name})
		}
		candidates = append(candidates, subjectBase{definition: def.Name, wildcard: true})
		for _, b := range candidates {
			if bases[b] {
				orderedBases = append(orderedBases, b)
			}
		}
	}

	knownTypes := map[string]bool{}
	var subjectRefTypes []SubjectRefTypeData
	for _, b := range orderedBases {
		var caveats []string
		var hasExpiration bool
		combined := map[string]bool{}
		for v := range variants {
			if v.base != b {
				continue
			}
			if v.caveat != "" && !contains(caveats, v.caveat) {
				caveats = append(caveats, v.caveat)
			}
			if v.caveat == "" && v.expiration {
				hasExpiration = true
			}
			if v.caveat != "" && v.expiration {
				combined[v.caveat] = true
			}
		}
		sort.Strings(caveats)

		emit := func(caveat string, expiration bool, withs []WithMethodData) {
			typeName := variantTypeName(b, caveat, expiration)
			n.claim("", typeName, "subject "+describe(b, caveat, expiration))
			knownTypes[typeName] = true
			srt := SubjectRefTypeData{
				TypeName:        typeName,
				DefinitionName:  b.definition,
				SubRelation:     b.relation,
				Wildcard:        b.wildcard,
				CaveatName:      caveat,
				HasCaveat:       caveat != "",
				HasExpiration:   expiration,
				IsDefinitionRef: b == subjectBase{definition: b.definition},
				WithMethods:     withs,
			}
			if caveat != "" {
				srt.CaveatContext = toPascalCase(caveat) + "Context"
			}
			subjectRefTypes = append(subjectRefTypes, srt)
		}

		var baseWiths []WithMethodData
		for _, c := range caveats {
			baseWiths = append(baseWiths, WithMethodData{
				MethodName:  "With" + toPascalCase(c),
				ContextType: toPascalCase(c) + "Context",
				ReturnType:  variantTypeName(b, c, false),
			})
		}
		if hasExpiration {
			baseWiths = append(baseWiths, WithMethodData{
				MethodName:   "WithExpiration",
				ReturnType:   variantTypeName(b, "", true),
				IsExpiration: true,
			})
		}
		emit("", false, baseWiths)
		for _, c := range caveats {
			var withs []WithMethodData
			if combined[c] {
				withs = append(withs, WithMethodData{
					MethodName:   "WithExpiration",
					ReturnType:   variantTypeName(b, c, true),
					IsExpiration: true,
					CarryContext: true,
				})
			}
			emit(c, false, withs)
		}
		if hasExpiration {
			emit("", true, nil)
		}
		for _, c := range caveats {
			if combined[c] {
				emit(c, true, nil)
			}
		}
	}

	// Sealed interfaces: one per relation (exact allowed variants), one per
	// relation key (identity-only base refs), and one per permission.
	var sealedInterfaces []SealedInterfaceData
	typeInterfaces := map[string][]string{}
	addInterface := func(name string, implementors []string) {
		n.claim("", name, "sealed interface "+name)
		marker := "is" + name
		sealedInterfaces = append(sealedInterfaces, SealedInterfaceData{Name: name, MarkerMethod: marker, Implementors: implementors})
		for _, impl := range implementors {
			typeInterfaces[impl] = append(typeInterfaces[impl], marker)
		}
	}

	// Type sentinels for direct and userset bases.
	var typeSentinels []TypeSentinelData

	var definitions []DefinitionData
	for _, def := range s.Definitions {
		pascal := toPascalCase(def.Name)
		refType := pascal + "Ref"
		dd := DefinitionData{
			Name:          def.Name,
			PascalName:    pascal,
			HasSubjectRef: bases[subjectBase{definition: def.Name}],
		}
		if !dd.HasSubjectRef {
			n.claim("", refType, "definition "+def.Name)
		}
		n.claim("", pascal, "constructor for definition "+def.Name)
		n.claim("", pascal+"Filter", "type filter for definition "+def.Name)
		n.claim("", pascal+"Relations", "relation names for definition "+def.Name)
		n.claim("", pascal+"Permissions", "permission names for definition "+def.Name)
		n.claim("", pascal+"RelationNames", "relation name list for definition "+def.Name)
		n.claim("", pascal+"PermissionNames", "permission name list for definition "+def.Name)
		if bases[subjectBase{definition: def.Name, wildcard: true}] {
			dd.WildcardType = subjectBase{definition: def.Name, wildcard: true}.refType()
			n.claim("", pascal+"Wildcard", "wildcard constructor for definition "+def.Name)
		}
		for _, m := range []string{"Filter", "PermissionByName", "RelationByName", "RelationshipByName", "String", "ID"} {
			n.claim(refType, m, "built-in method "+m)
		}

		// Sub-ref accessors. A relation userset's accessor takes the relation's
		// name and its writer moves to For<Name>. A permission userset's
		// accessor is <Name>Subject, since <Name> is the permission accessor.
		subRefRelations := map[string]bool{}
		for _, rel := range def.Relations {
			b := subjectBase{definition: def.Name, relation: rel.Name}
			if !bases[b] {
				continue
			}
			subRefRelations[rel.Name] = true
			dd.SubRefMethods = append(dd.SubRefMethods, SubRefMethodData{MethodName: toPascalCase(rel.Name), ReturnType: b.refType()})
		}
		for _, perm := range def.Permissions {
			b := subjectBase{definition: def.Name, relation: perm.Name}
			if !bases[b] {
				continue
			}
			dd.SubRefMethods = append(dd.SubRefMethods, SubRefMethodData{MethodName: toPascalCase(perm.Name) + "Subject", ReturnType: b.refType()})
		}
		for _, sr := range dd.SubRefMethods {
			n.claim(refType, sr.MethodName, "subject accessor returning "+sr.ReturnType)
		}

		for _, rel := range def.Relations {
			rp := toPascalCase(rel.Name)
			rd := RelationData{
				Name:          rel.Name,
				PascalName:    rp,
				InterfaceName: pascal + rp + "Subject",
				KeyMethod:     rp + "Key",
				KeyInterface:  pascal + rp + "KeySubject",
				CheckMethod:   rp + "Relation",
				FilterMethod:  rp + "Filter",
			}
			if subRefRelations[rel.Name] {
				rd.PascalName = "For" + rp
			}
			implementors := collectImplementors(rel.AllowedSubjects, knownTypes)
			var keyImplementors []string
			seen := map[string]bool{}
			for _, st := range rel.AllowedSubjects {
				t := baseOf(st).refType()
				if !seen[t] && knownTypes[t] {
					seen[t] = true
					keyImplementors = append(keyImplementors, t)
				}
			}
			sort.Strings(keyImplementors)
			if len(implementors) > 0 {
				rd.HasSubjects = true
				addInterface(rd.InterfaceName, implementors)
				n.claim(refType, rd.PascalName, "writer for relation "+rel.Name)
				n.claim(refType, rd.KeyMethod, "key for relation "+rel.Name)
			}
			if len(keyImplementors) > 0 {
				rd.HasKeyInterface = true
				addInterface(rd.KeyInterface, keyImplementors)
			}
			n.claim(refType, rd.CheckMethod, "check accessor for relation "+rel.Name)
			n.claim(refType, rd.FilterMethod, "filter for relation "+rel.Name)
			dd.Relations = append(dd.Relations, rd)
			dd.RelationNames = append(dd.RelationNames, NameData{Name: rel.Name, PascalName: rp})
		}

		for _, perm := range def.Permissions {
			pp := toPascalCase(perm.Name)
			pd := PermissionData{Name: perm.Name, PascalName: pp, InterfaceName: pascal + pp + "Subject"}
			if implementors := collectImplementors(perm.ReachableSubjects, knownTypes); len(implementors) > 0 {
				addInterface(pd.InterfaceName, implementors)
			}
			n.claim(refType, pp, "permission "+perm.Name)
			n.claim("", pascal+"_"+pp, "permission ref var for "+def.Name+"#"+perm.Name)
			dd.Permissions = append(dd.Permissions, pd)
			dd.PermissionKeys = append(dd.PermissionKeys, NameData{Name: perm.Name, PascalName: pp})
		}
		definitions = append(definitions, dd)
	}

	for _, b := range orderedBases {
		if b.wildcard {
			continue
		}
		sentinel := TypeSentinelData{
			VarName:        b.prefix() + "Type",
			InternalType:   toLowerCamel(b.definition) + toPascalCase(b.relation) + "RefType",
			DefinitionName: b.definition,
			SubRelation:    b.relation,
			MarkerMethods:  typeInterfaces[b.refType()],
		}
		n.claim("", sentinel.VarName, "type sentinel for "+describe(b, "", false))
		typeSentinels = append(typeSentinels, sentinel)
	}

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

	// Identifiers the static part of the template declares.
	for _, fixed := range []string{"Subject", "Permission", "PermissionRef", "TypedRelationship", "RelationshipKey",
		"Deletable", "TypedClient", "NewTypedClient", "Txn", "TypedCheck", "ChecksFor", "Check", "CheckBulk",
		"LookupResources", "LookupSubjects", "FromRelationship", "ErrRelationship", "WithSubject",
		"ErrNotInSchema", "ErrSubjectNotAllowed"} {
		n.claim("", fixed, "generated support code")
	}
	if n.err != nil {
		return TemplateData{}, n.err
	}

	return TemplateData{
		CaveatContextTypes: caveatContextTypes,
		SubjectRefTypes:    subjectRefTypes,
		TypeSentinels:      typeSentinels,
		Definitions:        definitions,
		SealedInterfaces:   sealedInterfaces,
		PermissionVars:     permissionVars,
	}, nil
}

func describe(b subjectBase, caveat string, expiration bool) string {
	s := b.definition
	switch {
	case b.wildcard:
		s += ":*"
	case b.relation != "":
		s += "#" + b.relation
	}
	if caveat != "" {
		s += " with " + caveat
	}
	if expiration {
		s += " with expiration"
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
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
