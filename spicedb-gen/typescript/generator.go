package typescript

import (
	"bytes"
	"embed"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
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

// DefaultClientModule is the module the generated file imports the native
// client from when --typescript.client-module is not set.
const DefaultClientModule = "@spicedb/client"

// TemplateData holds all pre-computed data needed by the template.
type TemplateData struct {
	ClientModule   string
	CaveatContexts []CaveatContextData
	RefTypes       []RefTypeData // standalone subject refs (usersets, wildcards, variants)
	Definitions    []DefinitionData
	CheckOverloads []OverloadData
	Lookups        []OverloadData
	UsersetTypes   []UsersetTypeData
}

// CaveatContextData represents a caveat context interface.
type CaveatContextData struct {
	InterfaceName     string            // e.g. "IpRangeContext"
	CaveatName        string            // e.g. "ip_range"
	ConverterFuncName string            // e.g. "ipRangeContextToRaw"
	Fields            []CaveatFieldData // e.g. [{Name: "allowedCidr", RawName: "allowed_cidr", TSType: "string"}]
}

// CaveatFieldData represents a single field of a caveat context interface.
type CaveatFieldData struct {
	Name    string // camelCase name, e.g. "allowedCidr"
	RawName string // original schema param name, e.g. "allowed_cidr" -- the
	// key SpiceDB's CEL evaluator actually looks up. Kept distinct from Name
	// so the generated raw-context converter can translate camelCase (the
	// TypeScript-facing identifier) back to the wire-level name.
	TSType string // TypeScript type, e.g. "string"
}

// RefTypeData describes one subject ref type: a base (direct object, userset
// or wildcard) or one of its caveated/expiring variants.
type RefTypeData struct {
	TypeName      string // e.g. "UserRef", "UserIpRangeRef", "TeamMemberRef", "UserWildcardRef"
	Maker         string // e.g. "makeUserIpRangeRef"
	Definition    string // e.g. "user"
	Relation      string // userset relation or permission, e.g. "member"
	Wildcard      bool
	Caveat        string
	ContextType   string
	Expiration    bool
	IsDefinition  bool // the direct base ref of a definition; merged into the definition's ref
	WithMethods   []WithMethodData
	TupleRelation string // "#member" or ""
}

// WithMethodData is a with* method converting a ref to a caveated or expiring variant.
type WithMethodData struct {
	MethodName   string // e.g. "withIpRange", "withExpiration"
	ContextType  string // e.g. "IpRangeContext"
	Converter    string // e.g. "ipRangeContextToRaw"
	ReturnType   string
	ReturnMaker  string
	IsExpiration bool
	CarryContext bool
}

// DefinitionData holds pre-computed data for a single definition.
type DefinitionData struct {
	Name            string // original name, e.g. "document"
	PascalName      string // e.g. "Document"
	RefType         string // e.g. "DocumentRef"
	Maker           string // e.g. "makeDocumentRef"
	WithMethods     []WithMethodData
	Permissions     []PermissionData
	Relations       []RelationData
	PermSubRefs     []SubRefData
	WildcardType    string
	WildcardMaker   string
	RelationNames   []NameData
	PermissionNames []NameData
}

// NameData pairs a schema name with its camelCase key.
type NameData struct {
	Name  string
	Camel string
}

// PermissionData describes a permission accessor.
type PermissionData struct {
	Name         string
	Prop         string // camelCase property, e.g. "canAdmin"
	CheckSubject string // union type name, e.g. "DocumentViewCheckSubject"
	CheckUnion   string
}

// RelationData describes the members generated for one relation.
type RelationData struct {
	Name          string
	Prop          string // writer, e.g. "viewer"
	SubjectType   string // e.g. "DocumentViewerSubject"
	SubjectUnion  string
	KeyMethod     string // e.g. "viewerKey"
	KeyType       string // e.g. "DocumentViewerKeySubject"
	KeyUnion      string
	CheckProp     string // e.g. "viewerRelation"
	CheckSubject  string
	CheckUnion    string
	FilterMethod  string // e.g. "viewerFilter"
	SubRefType    string // set when d#r is a subject: the no-argument overload returns it
	SubRefMaker   string
	AllowedRefs   []string
	HasSubjects   bool
	HasKeySubject bool
}

// SubRefData is a permission-userset accessor, e.g. canAdminSubject().
type SubRefData struct {
	Method     string
	ReturnType string
	Maker      string
}

// OverloadData is one check or lookup overload.
type OverloadData struct {
	ResourceType string
	Name         string
	SubjectUnion string
}

// UsersetTypeData is a sentinel naming a userset subject type for filters,
// e.g. OrganizationCanAdminType.
type UsersetTypeData struct {
	VarName    string
	Definition string
	Relation   string
}

var clientModuleRe = regexp.MustCompile(`^[A-Za-z0-9@._/~-]+$`)

// renameRe accepts the identifier stems --typescript.rename may set:
// PascalCase like every generated definition stem, so no stem can be a
// reserved word.
var renameRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// renameMu serializes Generate, which installs per-call definition renames
// for defIdent.
var (
	renameMu   sync.Mutex
	defRenames map[string]string
)

// defIdent is the TypeScript identifier stem for a schema definition: its
// PascalCase name, or the --typescript.rename override for it.
func defIdent(name string) string {
	if r, ok := defRenames[name]; ok {
		return r
	}
	return toPascalCase(name)
}

// parseRenames parses --typescript.rename=def=Name[,def=Name...].
func parseRenames(v string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		def, ident, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || def == "" || !renameRe.MatchString(ident) {
			return nil, fmt.Errorf("--typescript.rename entry %q must be <definition>=<PascalCaseName>", pair)
		}
		out[def] = ident
	}
	return out, nil
}

// Generate produces the generated TypeScript module from the parsed schema.
// Language options:
//   - "client-module" (--typescript.client-module): the module specifier the
//     generated file imports the native client from (default @spicedb/client).
//   - "format" (--typescript.format): "none" (default) emits the generator's
//     own normalized layout; "prettier" pipes the output through the Prettier
//     installed in the current directory's project (`npx --no-install
//     prettier`), honoring that project's Prettier configuration.
//   - "rename" (--typescript.rename=def=Name,...): the identifier stem for
//     definitions whose names would otherwise collide with generated
//     identifiers. Renames change TypeScript identifiers only; the schema
//     names sent to SpiceDB never change.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	module := DefaultClientModule
	format := "none"
	renames := map[string]string{}
	for k, v := range opts {
		switch k {
		case "client-module":
			if !clientModuleRe.MatchString(v) {
				return nil, fmt.Errorf("--typescript.client-module=%q is not a valid module specifier", v)
			}
			module = v
		case "format":
			if v != "none" && v != "prettier" {
				return nil, fmt.Errorf("--typescript.format=%q: must be none or prettier", v)
			}
			format = v
		case "rename":
			r, err := parseRenames(v)
			if err != nil {
				return nil, err
			}
			renames = r
		default:
			return nil, fmt.Errorf("unknown TypeScript option --typescript.%s", k)
		}
	}
	known := map[string]bool{}
	for _, def := range s.Definitions {
		known[def.Name] = true
	}
	for def := range renames {
		if !known[def] {
			return nil, fmt.Errorf("--typescript.rename names %q, which is not a definition in the schema", def)
		}
	}

	renameMu.Lock()
	defer renameMu.Unlock()
	defRenames = renames
	defer func() { defRenames = nil }()

	data, err := buildTemplateData(s)
	if err != nil {
		return nil, err
	}
	data.ClientModule = module

	tmpl, err := template.New("typed_client.ts.tmpl").Funcs(template.FuncMap{
		"quoteList": quoteList,
		"a":         withArticle,
	}).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}

	content := normalizeBlankLines(buf.Bytes())
	if format == "prettier" {
		if content, err = runPrettier(content); err != nil {
			return nil, err
		}
	}
	return []generator.GeneratedFile{
		{
			Path:    "typed_client.gen.ts",
			Content: content,
		},
	}, nil
}

// runPrettier formats TypeScript source with the project's own Prettier, so
// generated output matches the formatter configuration the project checks.
func runPrettier(src []byte) ([]byte, error) {
	cmd := exec.Command("npx", "--no-install", "prettier", "--stdin-filepath", "typed_client.gen.ts")
	cmd.Stdin = bytes.NewReader(src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("--typescript.format=prettier: running Prettier failed (install prettier in the project that receives the output): %w: %s", err, stderr.String())
	}
	return out, nil
}

// normalizeBlankLines collapses runs of blank lines and trailing spaces left
// by template control flow, so output is stable and formatter-friendly.
func normalizeBlankLines(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	var out []string
	blank := false
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, l)
	}
	return []byte(strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n")
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
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

func (b subjectBase) prefix() string {
	switch {
	case b.wildcard:
		return defIdent(b.definition) + "Wildcard"
	case b.relation != "":
		return defIdent(b.definition) + toPascalCase(b.relation)
	default:
		return defIdent(b.definition)
	}
}

func (b subjectBase) refType() string { return b.prefix() + "Ref" }

// variantTypeName names a subject variant <Prefix><Caveat>[Expiring]Ref.
func variantTypeName(b subjectBase, caveat string, expiration bool) string {
	name := b.prefix() + toPascalCase(caveat)
	if expiration {
		name += "Expiring"
	}
	return name + "Ref"
}

// subjectRefTypeName returns the TypeScript ref type name for a subject type.
func subjectRefTypeName(st schema.SubjectType) string {
	return variantTypeName(baseOf(st), st.CaveatName, st.Expiration)
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

type variantKey struct {
	base       subjectBase
	caveat     string
	expiration bool
}

// names detects collisions among generated identifiers. Module-level names
// share one scope; each definition ref has its own member scope.
type names struct {
	owners map[string]string
	err    error
}

func (n *names) claim(scope, name, owner string) {
	key := scope + "." + name
	if prev, ok := n.owners[key]; ok && prev != owner && n.err == nil {
		where := "module scope"
		if scope != "" {
			where = scope
		}
		n.err = fmt.Errorf("schema produces conflicting TypeScript identifier %s in %s (from %s and %s); "+
			"rename the schema element, or keep the schema and pass --typescript.rename=<definition>=<Name> to change a definition's TypeScript identifiers", name, where, prev, owner)
	}
	n.owners[key] = owner
}

// reservedModuleNames are identifiers the generated module declares, imports
// from the native client, or uses as globals; a schema-derived name equal to
// one would shadow it or conflict with its import (definition subject's
// SubjectRef, for example).
var reservedModuleNames = []string{
	"Permission", "PermissionRef", "TypedRelationship", "RelationshipKey", "TypedTransaction", "TypedClient", "TypedCheck",
	"NotInSchemaError", "SubjectNotAllowedError", "AnySubject", "SubjectType", "fromRelationship", "errRelationship",
	"checksFor", "withSubject", "withSubjectAnyRelation", "LookupResourcesOptions", "LookupSubjectsOptions", "ReadRelationshipsOptions",
	"SpiceDBClient", "createSpiceDBClient", "Transaction", "InvalidArgumentError", "SubjectRelationKind", "relationshipToFilter",
	"relationshipToString", "CheckResult", "CheckRequest", "Relationship", "RelationshipFilter", "SubjectRef", "LookupResource",
	"LookupSubject", "ReadRelationship", "CheckOptions", "CallOptions", "LookupResourcesRequest", "LookupSubjectsRequest",
	"ReadRelationshipsRequest", "SpiceDBClientOptions", "JSONObject", "JSONValue", "WriteResult", "RelationshipUpdate", "Precondition",
	"Consistency", "Object", "Array", "String", "Number", "Boolean", "Date", "Error", "JSON", "Map", "Set", "Symbol",
	"Promise", "Function", "RegExp", "BigInt", "Math", "Reflect", "Proxy", "Intl", "Record", "Readonly", "Partial",
}

// reservedRefMembers are members every definition ref declares.
var reservedRefMembers = []string{"_ref", "_type", "_id", "filter", "permissionByName", "relationByName", "relationshipByName", "toString", "toJSON", "constructor"}

// reservedStatics are properties a function object already has, which
// permission statics such as Document.view must not overwrite.
var reservedStatics = []string{"name", "length", "prototype", "caller", "arguments", "apply", "call", "bind", "toString"}

// buildTemplateData pre-computes all data needed by the template.
func buildTemplateData(s *schema.Schema) (TemplateData, error) {
	defs := map[string]schema.Definition{}
	for _, def := range s.Definitions {
		defs[def.Name] = def
	}

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
				if st.CaveatName != "" {
					variants[variantKey{baseOf(st), st.CaveatName, false}] = true
				}
			}
		}
	}
	for b := range bases {
		variants[variantKey{base: b}] = true
	}
	// A caveated, expiring subject is built as base.withCaveat(ctx).withExpiration(at),
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

	n := &names{owners: map[string]string{}}
	for _, r := range reservedModuleNames {
		n.claim("", r, "generated support code")
	}

	var caveatContexts []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		cc := CaveatContextData{
			InterfaceName:     toPascalCase(c.Name) + "Context",
			CaveatName:        c.Name,
			ConverterFuncName: toCamelCase(c.Name) + "ContextToRaw",
		}
		n.claim("", cc.InterfaceName, "context for caveat "+c.Name)
		for _, p := range c.Params {
			cc.Fields = append(cc.Fields, CaveatFieldData{
				Name:    toCamelCase(p.Name),
				RawName: p.Name,
				TSType:  celTypeToTS(p.Type),
			})
		}
		caveatContexts = append(caveatContexts, cc)
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
	var refTypes []RefTypeData
	definitionWiths := map[string][]WithMethodData{}
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
			isDef := b == subjectBase{definition: b.definition} && caveat == "" && !expiration
			n.claim("", typeName, "subject "+describe(b, caveat, expiration))
			knownTypes[typeName] = true
			if isDef {
				definitionWiths[b.definition] = withs
				return
			}
			rt := RefTypeData{
				TypeName:     typeName,
				Maker:        "make" + typeName,
				Definition:   b.definition,
				Relation:     b.relation,
				Wildcard:     b.wildcard,
				Caveat:       caveat,
				Expiration:   expiration,
				WithMethods:  withs,
				IsDefinition: false,
			}
			if b.relation != "" {
				rt.TupleRelation = "#" + b.relation
			}
			if caveat != "" {
				rt.ContextType = toPascalCase(caveat) + "Context"
			}
			refTypes = append(refTypes, rt)
		}

		var baseWiths []WithMethodData
		for _, c := range caveats {
			t := variantTypeName(b, c, false)
			baseWiths = append(baseWiths, WithMethodData{
				MethodName:  "with" + toPascalCase(c),
				ContextType: toPascalCase(c) + "Context",
				Converter:   toCamelCase(c) + "ContextToRaw",
				ReturnType:  t,
				ReturnMaker: "make" + t,
			})
		}
		if hasExpiration {
			t := variantTypeName(b, "", true)
			baseWiths = append(baseWiths, WithMethodData{MethodName: "withExpiration", ReturnType: t, ReturnMaker: "make" + t, IsExpiration: true})
		}
		emit("", false, baseWiths)
		for _, c := range caveats {
			var withs []WithMethodData
			if combined[c] {
				t := variantTypeName(b, c, true)
				withs = append(withs, WithMethodData{MethodName: "withExpiration", ReturnType: t, ReturnMaker: "make" + t, IsExpiration: true, CarryContext: true})
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

	// checkSubjects returns the refs a check may name as its subject: each
	// reachable subject without expiration (a check has none), a wildcard
	// as its concrete base type (SpiceDB does not check a wildcard
	// subject, but type:* grants every concrete one), and usersets
	// expanded to the subjects that can be their members.
	var checkSubjects func(subjects []schema.SubjectType, seen map[subjectBase]bool) []string
	checkSubjects = func(subjects []schema.SubjectType, seen map[subjectBase]bool) []string {
		var out []string
		for _, st := range subjects {
			b := baseOf(st)
			if st.Wildcard {
				// type:* grants every concrete subject of the type.
				b = subjectBase{definition: st.Definition}
			}
			name := variantTypeName(b, st.CaveatName, false)
			if knownTypes[name] {
				out = append(out, name)
			}
			if st.Relation != "" && !seen[baseOf(st)] {
				seen[baseOf(st)] = true
				target := defs[st.Definition]
				for _, r := range target.Relations {
					if r.Name == st.Relation {
						out = append(out, checkSubjects(r.AllowedSubjects, seen)...)
					}
				}
				for _, p := range target.Permissions {
					if p.Name == st.Relation {
						out = append(out, checkSubjects(p.ReachableSubjects, seen)...)
					}
				}
			}
		}
		return out
	}

	var definitions []DefinitionData
	var checks, lookups []OverloadData
	var usersets []UsersetTypeData
	for _, def := range s.Definitions {
		pascal := defIdent(def.Name)
		refType := pascal + "Ref"
		scope := refType
		dd := DefinitionData{
			Name:        def.Name,
			PascalName:  pascal,
			RefType:     refType,
			Maker:       "make" + refType,
			WithMethods: definitionWiths[def.Name],
		}
		if !knownTypes[refType] {
			n.claim("", refType, "definition "+def.Name)
		}
		for _, suffix := range []string{"", "Filter", "Relations", "Permissions", "RelationNames", "PermissionNames", "RelationName", "PermissionName"} {
			n.claim("", pascal+suffix, "generated "+suffix+" for definition "+def.Name)
		}
		for _, m := range reservedRefMembers {
			n.claim(scope, m, "built-in member "+m)
		}
		for _, w := range dd.WithMethods {
			n.claim(scope, w.MethodName, "subject variant method "+w.MethodName)
		}
		if bases[subjectBase{definition: def.Name, wildcard: true}] {
			dd.WildcardType = subjectBase{definition: def.Name, wildcard: true}.refType()
			dd.WildcardMaker = "make" + dd.WildcardType
			n.claim("", pascal+"Wildcard", "wildcard constructor for definition "+def.Name)
		}

		for _, perm := range def.Permissions {
			b := subjectBase{definition: def.Name, relation: perm.Name}
			if bases[b] {
				sr := SubRefData{Method: toCamelCase(perm.Name) + "Subject", ReturnType: b.refType(), Maker: "make" + b.refType()}
				n.claim(scope, sr.Method, "subject accessor for permission userset "+def.Name+"#"+perm.Name)
				dd.PermSubRefs = append(dd.PermSubRefs, sr)
				usersets = append(usersets, UsersetTypeData{VarName: b.prefix() + "Type", Definition: def.Name, Relation: perm.Name})
			}
		}

		for _, rel := range def.Relations {
			camel := toCamelCase(rel.Name)
			rp := toPascalCase(rel.Name)
			rd := RelationData{
				Name:         rel.Name,
				Prop:         camel,
				SubjectType:  pascal + rp + "Subject",
				KeyMethod:    camel + "Key",
				KeyType:      pascal + rp + "KeySubject",
				CheckProp:    camel + "Relation",
				CheckSubject: pascal + rp + "RelationCheckSubject",
				FilterMethod: camel + "Filter",
			}
			if b := (subjectBase{definition: def.Name, relation: rel.Name}); bases[b] {
				rd.SubRefType = b.refType()
				rd.SubRefMaker = "make" + b.refType()
				usersets = append(usersets, UsersetTypeData{VarName: b.prefix() + "Type", Definition: def.Name, Relation: rel.Name})
			}
			implementors := collectImplementors(rel.AllowedSubjects, knownTypes, true)
			rd.AllowedRefs = implementors
			// A key is distinct from the writer only when some admitted
			// subject carries a caveat or expiration; otherwise the writer's
			// TypedRelationship (and its key()) already identifies the tuple.
			needsKey := false
			for _, st := range rel.AllowedSubjects {
				if st.CaveatName != "" || st.Expiration {
					needsKey = true
				}
			}
			var keys []string
			for _, st := range rel.AllowedSubjects {
				if t := baseOf(st).refType(); needsKey && knownTypes[t] && !contains(keys, t) {
					keys = append(keys, t)
				}
			}
			if len(implementors) > 0 {
				rd.HasSubjects = true
				rd.SubjectUnion = strings.Join(implementors, " | ")
				n.claim("", rd.SubjectType, "subjects of relation "+def.Name+"#"+rel.Name)
			}
			if len(keys) > 0 {
				rd.HasKeySubject = true
				rd.KeyUnion = strings.Join(keys, " | ")
				n.claim("", rd.KeyType, "key subjects of relation "+def.Name+"#"+rel.Name)
			}
			rd.CheckUnion = unionOrNever(dedupe(checkSubjects(rel.AllowedSubjects, map[subjectBase]bool{})))
			n.claim("", rd.CheckSubject, "check subjects of relation "+def.Name+"#"+rel.Name)
			owner := "relation " + rel.Name
			n.claim(scope, rd.Prop, owner)
			if rd.HasKeySubject {
				n.claim(scope, rd.KeyMethod, "key for "+owner)
			}
			n.claim(scope, rd.CheckProp, "check accessor for "+owner)
			n.claim(scope, rd.FilterMethod, "filter for "+owner)
			checks = append(checks, OverloadData{ResourceType: def.Name, Name: rel.Name, SubjectUnion: rd.CheckSubject})
			dd.Relations = append(dd.Relations, rd)
			dd.RelationNames = append(dd.RelationNames, NameData{Name: rel.Name, Camel: camel})
		}

		for _, perm := range def.Permissions {
			camel := toCamelCase(perm.Name)
			pd := PermissionData{
				Name:         perm.Name,
				Prop:         camel,
				CheckSubject: pascal + toPascalCase(perm.Name) + "CheckSubject",
				CheckUnion:   unionOrNever(dedupe(checkSubjects(perm.ReachableSubjects, map[subjectBase]bool{}))),
			}
			n.claim(scope, pd.Prop, "permission "+perm.Name)
			n.claim("", pd.CheckSubject, "check subjects of permission "+def.Name+"#"+perm.Name)
			if contains(reservedStatics, camel) {
				n.claim("", pascal+"."+camel, "built-in function property "+camel)
				n.claim("", pascal+"."+camel, "lookup static for permission "+def.Name+"#"+perm.Name)
			}
			checks = append(checks, OverloadData{ResourceType: def.Name, Name: perm.Name, SubjectUnion: pd.CheckSubject})
			lookups = append(lookups, OverloadData{ResourceType: def.Name, Name: perm.Name, SubjectUnion: pd.CheckSubject})
			dd.Permissions = append(dd.Permissions, pd)
			dd.PermissionNames = append(dd.PermissionNames, NameData{Name: perm.Name, Camel: camel})
		}
		definitions = append(definitions, dd)
	}
	for _, u := range usersets {
		n.claim("", u.VarName, "userset type sentinel for "+u.Definition+"#"+u.Relation)
	}
	if n.err != nil {
		return TemplateData{}, n.err
	}

	return TemplateData{
		CaveatContexts: caveatContexts,
		RefTypes:       refTypes,
		Definitions:    definitions,
		CheckOverloads: checks,
		Lookups:        lookups,
		UsersetTypes:   usersets,
	}, nil
}

func unionOrNever(items []string) string {
	if len(items) == 0 {
		return "never"
	}
	return strings.Join(items, " | ")
}

func dedupe(items []string) []string {
	var out []string
	for _, s := range items {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// collectImplementors returns the ref types for the given subject types, in
// schema order, deduplicated.
func collectImplementors(subjects []schema.SubjectType, knownTypes map[string]bool, exact bool) []string {
	var result []string
	for _, st := range subjects {
		name := subjectRefTypeName(st)
		if !exact {
			name = variantTypeName(baseOf(st), st.CaveatName, false)
		}
		if knownTypes[name] && !contains(result, name) {
			result = append(result, name)
		}
	}
	return result
}

// buildSubjectUnion builds a TypeScript union type string from a list of subject types.
func buildSubjectUnion(subjects []schema.SubjectType) string {
	var parts []string
	for _, st := range subjects {
		if name := subjectRefTypeName(st); !contains(parts, name) {
			parts = append(parts, name)
		}
	}
	return unionOrNever(parts)
}

// withArticle prefixes word with "a" or "an" for generated comments ("an
// organization", "a document", "a user"). The choice follows the first
// letter, except for common words whose first sound is not that letter's.
func withArticle(word string) string {
	lower := strings.ToLower(word)
	for _, p := range []string{"uni", "use", "usa", "usu", "uti", "ure", "euro", "one", "once"} {
		if strings.HasPrefix(lower, p) {
			return "a " + word
		}
	}
	for _, p := range []string{"hour", "honest", "honor", "heir"} {
		if strings.HasPrefix(lower, p) {
			return "an " + word
		}
	}
	if lower != "" && strings.ContainsRune("aeiou", rune(lower[0])) {
		return "an " + word
	}
	return "a " + word
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
	pascal := toPascalCase(s)
	if pascal == "" {
		return ""
	}
	return strings.ToLower(pascal[:1]) + pascal[1:]
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
	case "map":
		return "JSONObject"
	case "list":
		return "readonly JSONValue[]"
	default:
		return "JSONValue"
	}
}

// Options lists the options this generator accepts, for the CLI's help.
func (g *Generator) Options() []generator.Option {
	return []generator.Option{
		{Name: "client-module", Value: "<specifier>", Description: `Module specifier the generated file imports the native client from (default "@spicedb/client").`},
		{Name: "format", Value: "none|prettier", Description: `Format the output with the project's Prettier (default none).`},
		{Name: "rename", Value: "<definition>=<Name>[,...]", Description: `Identifier stem for a definition whose generated names collide; schema names on the wire are unchanged.`},
	}
}
