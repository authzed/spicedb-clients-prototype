package python

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
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

// DefaultModule is the name of the emitted types module (permissions.py).
const DefaultModule = "permissions"

// Generator implements generator.LanguageGenerator for Python.
type Generator struct{}

// Language returns "python".
func (g *Generator) Language() string { return "python" }

// TemplateData holds all pre-computed data needed by the Python templates.
type TemplateData struct {
	// Module is the types module's name; sync.py and aio.py import it as
	// `from .<Module> import (...)`.
	Module string

	CaveatContexts []CaveatContextData
	Definitions    []DefinitionData
	SubjectRefs    []SubjectRefData
	SealedUnions   []SealedUnionData
	Checkables     []CheckableData
	StaticPermRefs []StaticPermRefData

	// ClientTypeImports lists the names, sorted and deduplicated, that
	// sync.py/aio.py reference from the types module.
	ClientTypeImports []string
}

// CheckableData is a narrowed Permission subclass: one per permission and one
// per relation (relation checks). Its subject union drives the check and
// lookup_subjects overloads and the runtime admission check.
type CheckableData struct {
	ClassName    string // e.g. "_DocumentViewPermission" or "_OrganizationMemberRelation"
	RefClassName string // e.g. "_DocumentViewRef"; empty for relation checks
	SubjectUnion string // e.g. "DocumentViewSubject"; empty when nothing is admitted
	LookupTypes  string // e.g. "DocumentViewSubjectType": the lookup_subjects type argument
	Describe     string // e.g. "document#view"
}

type DefinitionData struct {
	ClassName   string // e.g. "Document"
	Name        string // e.g. "document"
	WithMethods []WithMethodData
	SubRefs     []DefSubRefData
	Permissions []DefPermissionData
	Relations   []DefRelationData

	RelationsClass   string // e.g. "DocumentRelations"
	PermissionsClass string // e.g. "DocumentPermissions"
	RelationNames    string // e.g. "DOCUMENT_RELATION_NAMES"
	PermissionNames  string // e.g. "DOCUMENT_PERMISSION_NAMES"

	RelationNameList   []string
	PermissionNameList []string
}

type DefPermissionData struct {
	PropName  string // e.g. "view" or "del_" if escaped
	ClassName string // e.g. "_DocumentViewPermission"
	Name      string // raw schema name
	ConstName string // e.g. "VIEW"
}

type DefRelationData struct {
	Name          string // raw schema name
	ConstName     string // e.g. "VIEWER"
	WriterName    string // e.g. "viewer", or "for_member" when a sub-ref takes the name
	SubjectUnion  string // e.g. "DocumentViewerSubject"; empty when no subject is admitted
	KeyName       string // e.g. "viewer_key"
	KeyUnion      string // e.g. "DocumentViewerKeySubject"
	CheckName     string // e.g. "viewer_relation"
	CheckClass    string // e.g. "_DocumentViewerRelation"
	FilterName    string // e.g. "viewer_filter"
	HasSubjects   bool
	HasKeySubject bool
}

type DefSubRefData struct {
	PropName   string // e.g. "member" or "can_admin_subject"
	ReturnType string // e.g. "TeamMember"
}

type StaticPermRefData struct {
	ConstName    string // e.g. "DocumentView"
	RefClassName string // e.g. "_DocumentViewRef"
	ResourceType string
	Permission   string
}

type SealedUnionData struct {
	AliasName string
	Members   []string
}

type CaveatContextData struct {
	ClassName  string // e.g. "IpRangeContext"
	CaveatName string
	Params     []CaveatParamData
}

type CaveatParamData struct {
	FieldName string // Python identifier, escaped if needed
	RawName   string // raw schema name: the CEL context key
	PyType    string
}

// SubjectRefData is a subject class other than a definition's own class: a
// userset ("TeamMember"), a wildcard ("UserWildcard"), or a caveat/expiration
// variant of any base ("UserIpRange", "UserExpiring", "OrganizationCanAdminExpiring").
type SubjectRefData struct {
	ClassName      string
	DefinitionName string
	SubRelation    string
	Wildcard       bool
	CaveatName     string
	CaveatContext  string
	HasCaveat      bool
	HasExpiration  bool
	Describe       string
	WithMethods    []WithMethodData
}

type WithMethodData struct {
	MethodName string // "with_ip_range" or "with_expiration"
	ParamName  string // "ctx" or "expiration"
	ParamType  string // "IpRangeContext" or "datetime"
	ReturnType string
	Args       []string // constructor arguments for ReturnType
}

// clientFlavor parameterizes client.py.tmpl, which renders both sync.py and
// aio.py so the two clients cannot drift apart.
type clientFlavor struct {
	TemplateData
	Async  bool
	Flavor string // "sync" or "aio"
	Def    string // "def" or "async def"
	Await  string // "" or "await "
	Stream string // "Stream" or "AsyncStream"
}

var pyIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Generate produces <module>.py (default permissions.py), sync.py and aio.py.
// The types module holds the flavor-agnostic types; sync.py and aio.py each
// define a TypedClient bound to spicedb.sync/aio.SpiceDBClient.
//
// Options (--python.<key>=<value>):
//   - module: the types module's name (default "permissions").
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	module := DefaultModule
	for k, v := range opts {
		switch k {
		case "module":
			if !pyIdentifier.MatchString(v) || pyHardKeywords[v] || v == "sync" || v == "aio" {
				return nil, fmt.Errorf("--python.module=%q must be a Python identifier other than a keyword, \"sync\" or \"aio\"", v)
			}
			module = v
		default:
			return nil, fmt.Errorf("unknown Python option --python.%s", k)
		}
	}

	data, err := buildTemplateData(s)
	if err != nil {
		return nil, err
	}
	data.Module = module

	tmpl, err := template.New("permissions.py.tmpl").Funcs(template.FuncMap{
		"alias":      formatAlias,
		"bracket":    bracket,
		"tuple":      tuple,
		"assign":     assign,
		"assignCall": assignCall,
		"list":       list,
		"quote":      quote,
	}).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var types bytes.Buffer
	if err := tmpl.ExecuteTemplate(&types, "permissions.py.tmpl", data); err != nil {
		return nil, fmt.Errorf("executing template permissions.py.tmpl: %w", err)
	}
	files := []generator.GeneratedFile{{Path: module + ".py", Content: tidy(types.Bytes())}}
	for _, flavor := range []clientFlavor{
		{TemplateData: data, Flavor: "sync", Def: "def", Stream: "Stream"},
		{TemplateData: data, Async: true, Flavor: "aio", Def: "async def", Await: "await ", Stream: "AsyncStream"},
	} {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "client.py.tmpl", flavor); err != nil {
			return nil, fmt.Errorf("executing template client.py.tmpl (%s): %w", flavor.Flavor, err)
		}
		files = append(files, generator.GeneratedFile{Path: flavor.Flavor + ".py", Content: tidy(buf.Bytes())})
	}
	return files, nil
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

// tidy normalizes the blank lines template control flow leaves behind: at most
// two consecutive blank lines, one trailing newline, and no trailing spaces.
// The templates themselves follow ruff format's layout.
func tidy(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := blankRuns.ReplaceAllString(strings.Join(lines, "\n"), "\n\n\n")
	return []byte(strings.TrimRight(out, "\n") + "\n")
}

// lineLength is ruff format's default line length.
const lineLength = 88

// The helpers below lay out the schema-dependent lines of the generated code
// the way `ruff format` (line length 88) does, so output is format-clean
// without running ruff. Each handles one construct whose parts contain no
// nested brackets; fixed template text is written pre-formatted.

// formatAlias renders `name: TypeAlias = A | B`: on one line when it fits,
// else parenthesized on one line, else one member per line. A lone subscript
// member (`type[X]`) splits on its own bracket instead.
func formatAlias(name string, members []string) string {
	lhs := name + ": TypeAlias"
	if len(members) == 1 && strings.HasPrefix(members[0], "type[") {
		inner := strings.TrimSuffix(strings.TrimPrefix(members[0], "type["), "]")
		one := lhs + " = " + members[0]
		if len(one) <= lineLength {
			return one
		}
		return lhs + " = type[\n    " + inner + "\n]"
	}
	one := lhs + " = " + strings.Join(members, " | ")
	if len(one) <= lineLength {
		return one
	}
	inner := "    " + strings.Join(members, " | ")
	if len(inner) <= lineLength {
		return lhs + " = (\n" + inner + "\n)"
	}
	return lhs + " = (\n    " + strings.Join(members, "\n    | ") + "\n)"
}

// bracket renders `<prefix>(<items>)<suffix>` at indent: on one line when it
// fits; else the items on one indented line; else one item per line with a
// trailing comma. A def's lone parameter always gets a trailing comma when
// split, as ruff adds one.
func bracket(indent int, prefix string, items []string, suffix string) string {
	ind := strings.Repeat(" ", indent)
	one := ind + prefix + "(" + strings.Join(items, ", ") + ")" + suffix
	if len(one) <= lineLength {
		return one
	}
	isDef := strings.HasPrefix(prefix, "def ") || strings.HasPrefix(prefix, "async def ")
	inner := ind + "    " + strings.Join(items, ", ")
	if len(inner) <= lineLength && !(isDef && len(items) == 1) {
		return ind + prefix + "(\n" + inner + "\n" + ind + ")" + suffix
	}
	var b strings.Builder
	b.WriteString(ind + prefix + "(\n")
	for _, item := range items {
		b.WriteString(ind + "    " + item + ",\n")
	}
	b.WriteString(ind + ")" + suffix)
	return b.String()
}

// assign renders `<lhs> = <rhs>` at indent, parenthesizing a plain rhs that
// does not fit.
func assign(indent int, lhs, rhs string) string {
	ind := strings.Repeat(" ", indent)
	one := ind + lhs + " = " + rhs
	if len(one) <= lineLength {
		return one
	}
	return ind + lhs + " = (\n" + ind + "    " + rhs + "\n" + ind + ")"
}

// assignCall renders `<lhs> = <callee>(<args>)` at module level: split inside
// the call's brackets when its opening line fits, else parenthesize the call.
func assignCall(lhs, callee string, args []string) string {
	head := lhs + " = " + callee + "("
	if len(head) <= lineLength {
		return bracket(0, lhs+" = "+callee, args, "")
	}
	return lhs + " = (\n" + bracket(4, callee, args, "") + "\n)"
}

// tuple renders `<lhs> = (<string literals>)`: inline when empty, inline for
// a single item that fits, otherwise one item per line with a trailing comma.
func tuple(lhs string, items []string) string {
	switch {
	case len(items) == 0:
		return lhs + " = ()"
	case len(items) == 1 && len(lhs+` = ("`+items[0]+`",)`) <= lineLength:
		return lhs + ` = ("` + items[0] + `",)`
	}
	var b strings.Builder
	b.WriteString(lhs + " = (\n")
	for _, item := range items {
		b.WriteString(`    "` + item + "\",\n")
	}
	b.WriteString(")")
	return b.String()
}

func typeArgs(members []string) []string {
	out := make([]string, len(members))
	for i, m := range members {
		out[i] = "type[" + m + "]"
	}
	return out
}

func list(items ...string) []string { return items }

func quote(s string) string { return `"` + s + `"` }

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

// className is the Python class of the base: the definition's own class for a
// direct object, <Def><Rel> for a userset, <Def>Wildcard for a wildcard.
func (b subjectBase) className() string {
	switch {
	case b.wildcard:
		return toPascalCase(b.definition) + "Wildcard"
	case b.relation != "":
		return toPascalCase(b.definition) + toPascalCase(b.relation)
	default:
		return toPascalCase(b.definition)
	}
}

// variantClassName is <Base>[<Caveat>][Expiring].
func variantClassName(b subjectBase, caveat string, expiration bool) string {
	name := b.className() + toPascalCase(caveat)
	if expiration {
		name += "Expiring"
	}
	return name
}

// pySubjectRefName returns the Python class name for an exact subject type.
func pySubjectRefName(st schema.SubjectType) string {
	return variantClassName(baseOf(st), st.CaveatName, st.Expiration)
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
// share one scope; each definition class has its own member scope. No
// identifier is ever silently renamed.
type names struct {
	owners map[string]string
	err    error
}

func (n *names) claim(scope, name, owner string) {
	key := scope + "." + name
	if prev, ok := n.owners[key]; ok && prev != owner && n.err == nil {
		where := "module scope"
		if scope != "" {
			where = "class " + scope
		}
		n.err = fmt.Errorf("schema produces conflicting Python identifier %s in %s (from %s and from %s); rename one of them", name, where, prev, owner)
	}
	n.owners[key] = owner
}

func buildTemplateData(s *schema.Schema) (TemplateData, error) {
	defsByName := map[string]schema.Definition{}
	for _, def := range s.Definitions {
		defsByName[def.Name] = def
	}

	// Every exact subject variant a relation admits, plus every base a
	// permission can reach (usersets on permissions need classes too).
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
	// A caveated, expiring subject is built as Base.with_<caveat>(ctx).with_expiration(t),
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
	for _, fixed := range []string{
		"NotInSchemaError", "SubjectNotAllowedError", "Subject", "Permission", "PermissionRef",
		"TypedRelationship", "RelationshipKey", "TypedTransaction", "checks_for", "with_subject",
		"annotations", "dataclass", "datetime", "replace", "json", "Any", "ClassVar", "Final",
		"Protocol", "TypeAlias", "Caveat", "Filter", "InvalidArgumentError", "ObjectRef",
		"PermissionCheck", "Relationship", "SubjectFilter", "SubjectRef", "SubjectRelationFilter",
		"Transaction",
	} {
		n.claim("", fixed, "generated support code")
	}

	var caveatContexts []CaveatContextData
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		cc := CaveatContextData{ClassName: toPascalCase(c.Name) + "Context", CaveatName: c.Name}
		n.claim("", cc.ClassName, "context for caveat "+c.Name)
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

	// withMethods returns the with_* builders of the class for (b, caveat,
	// expiration=false), and fields lists the constructor arguments.
	caveatsOf := func(b subjectBase) (caveats []string, expiring bool, combined map[string]bool) {
		combined = map[string]bool{}
		for v := range variants {
			if v.base != b {
				continue
			}
			if v.caveat != "" && !contains(caveats, v.caveat) {
				caveats = append(caveats, v.caveat)
			}
			if v.caveat == "" && v.expiration {
				expiring = true
			}
			if v.caveat != "" && v.expiration {
				combined[v.caveat] = true
			}
		}
		sort.Strings(caveats)
		return caveats, expiring, combined
	}
	idArg := func(b subjectBase, rest ...string) []string {
		if b.wildcard {
			return rest
		}
		return append([]string{"id=self.id"}, rest...)
	}
	baseWithMethods := func(b subjectBase) []WithMethodData {
		caveats, expiring, _ := caveatsOf(b)
		var withs []WithMethodData
		for _, c := range caveats {
			withs = append(withs, WithMethodData{
				MethodName: "with_" + c,
				ParamName:  "ctx",
				ParamType:  toPascalCase(c) + "Context",
				ReturnType: variantClassName(b, c, false),
				Args:       idArg(b, "context=ctx"),
			})
		}
		if expiring {
			withs = append(withs, WithMethodData{
				MethodName: "with_expiration",
				ParamName:  "expiration",
				ParamType:  "datetime",
				ReturnType: variantClassName(b, "", true),
				Args:       idArg(b, "expiration=expiration"),
			})
		}
		return withs
	}

	// Ordered bases: per definition, usersets on its relations and
	// permissions, then its wildcard. The direct base is the definition class.
	var orderedBases []subjectBase
	for _, def := range s.Definitions {
		var candidates []subjectBase
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

	known := map[string]bool{}
	for _, def := range s.Definitions {
		known[toPascalCase(def.Name)] = true
	}

	var subjectRefs []SubjectRefData
	emit := func(b subjectBase, caveat string, expiration bool, withs []WithMethodData) {
		name := variantClassName(b, caveat, expiration)
		n.claim("", name, "subject "+describe(b, caveat, expiration))
		known[name] = true
		sr := SubjectRefData{
			ClassName:      name,
			DefinitionName: b.definition,
			SubRelation:    b.relation,
			Wildcard:       b.wildcard,
			CaveatName:     caveat,
			HasCaveat:      caveat != "",
			HasExpiration:  expiration,
			Describe:       describe(b, caveat, expiration),
			WithMethods:    withs,
		}
		if caveat != "" {
			sr.CaveatContext = toPascalCase(caveat) + "Context"
		}
		subjectRefs = append(subjectRefs, sr)
	}
	variantsOf := func(b subjectBase, includeBase bool) {
		caveats, expiring, combined := caveatsOf(b)
		if includeBase {
			emit(b, "", false, baseWithMethods(b))
		}
		for _, c := range caveats {
			var withs []WithMethodData
			if combined[c] {
				args := idArg(b, "context=self.context", "expiration=expiration")
				withs = append(withs, WithMethodData{
					MethodName: "with_expiration",
					ParamName:  "expiration",
					ParamType:  "datetime",
					ReturnType: variantClassName(b, c, true),
					Args:       args,
				})
			}
			emit(b, c, false, withs)
		}
		if expiring {
			emit(b, "", true, nil)
		}
		for _, c := range caveats {
			if combined[c] {
				emit(b, c, true, nil)
			}
		}
	}
	for _, def := range s.Definitions {
		// The definition class is the direct base; only its variants are separate classes.
		variantsOf(subjectBase{definition: def.Name}, false)
	}
	for _, b := range orderedBases {
		variantsOf(b, true)
	}

	// closure returns the subject types reachable from subjects, following
	// usersets through the relation or permission they name: a check of
	// invitation#accept may be made for any user who reaches it, not only for
	// the organization#can_admin userset it grants.
	var closure func(subjects []schema.SubjectType, visited map[subjectBase]bool) []schema.SubjectType
	closure = func(subjects []schema.SubjectType, visited map[subjectBase]bool) []schema.SubjectType {
		out := append([]schema.SubjectType(nil), subjects...)
		for _, st := range subjects {
			if st.Relation == "" || visited[baseOf(st)] {
				continue
			}
			visited[baseOf(st)] = true
			def := defsByName[st.Definition]
			for _, rel := range def.Relations {
				if rel.Name == st.Relation {
					out = append(out, closure(rel.AllowedSubjects, visited)...)
				}
			}
			for _, perm := range def.Permissions {
				if perm.Name == st.Relation {
					out = append(out, closure(perm.ReachableSubjects, visited)...)
				}
			}
		}
		return out
	}

	// checkMembers lists the subject classes a check (of a permission or a
	// relation) admits: each reachable base (a wildcard admits its concrete
	// objects) and caveated variants, which supply check context. Expiration
	// does not apply to a check, so expiring variants are not admitted.
	checkMembers := func(subjects []schema.SubjectType) (members, lookup []string) {
		seen, lseen := map[string]bool{}, map[string]bool{}
		add := func(set map[string]bool, list *[]string, name string) {
			if known[name] && !set[name] {
				set[name] = true
				*list = append(*list, name)
			}
		}
		for _, st := range closure(subjects, map[subjectBase]bool{}) {
			b := baseOf(st)
			if b.wildcard {
				b = subjectBase{definition: b.definition}
			}
			add(seen, &members, b.className())
			add(lseen, &lookup, b.className())
			if st.CaveatName != "" {
				add(seen, &members, variantClassName(b, st.CaveatName, false))
			}
		}
		sort.Strings(members)
		sort.Strings(lookup)
		return members, lookup
	}

	exactMembers := func(subjects []schema.SubjectType) []string {
		seen := map[string]bool{}
		var out []string
		for _, st := range subjects {
			name := pySubjectRefName(st)
			if known[name] && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	keyMembers := func(subjects []schema.SubjectType) []string {
		seen := map[string]bool{}
		var out []string
		for _, st := range subjects {
			name := baseOf(st).className()
			if known[name] && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}

	var sealedUnions []SealedUnionData
	addUnion := func(name, owner string, members []string) string {
		if len(members) == 0 {
			return ""
		}
		n.claim("", name, owner)
		sealedUnions = append(sealedUnions, SealedUnionData{AliasName: name, Members: members})
		return name
	}

	// Definitions (resource side, merged with the direct subject).
	var definitions []DefinitionData
	var checkables []CheckableData
	var staticPermRefs []StaticPermRefData
	for _, def := range s.Definitions {
		pascal := toPascalCase(def.Name)
		upper := strings.ToUpper(def.Name)
		dd := DefinitionData{
			ClassName:        pascal,
			Name:             def.Name,
			WithMethods:      baseWithMethods(subjectBase{definition: def.Name}),
			RelationsClass:   pascal + "Relations",
			PermissionsClass: pascal + "Permissions",
			RelationNames:    upper + "_RELATION_NAMES",
			PermissionNames:  upper + "_PERMISSION_NAMES",
		}
		n.claim("", pascal, "definition "+def.Name)
		n.claim("", dd.RelationsClass, "relation names of "+def.Name)
		n.claim("", dd.PermissionsClass, "permission names of "+def.Name)
		n.claim("", dd.RelationNames, "relation name list of "+def.Name)
		n.claim("", dd.PermissionNames, "permission name list of "+def.Name)
		for _, m := range []string{"id", "type_filter", "filter", "permission_by_name", "relation_by_name", "relationship_by_name"} {
			n.claim(pascal, m, "built-in member "+m)
		}
		for _, w := range dd.WithMethods {
			n.claim(pascal, w.MethodName, "subject builder "+w.MethodName)
		}

		// Sub-ref accessors. A relation userset's accessor takes the
		// relation's name and its writer moves to for_<name>. A permission
		// userset's accessor is <name>_subject, since <name> is the
		// permission accessor.
		subRefRelations := map[string]bool{}
		for _, rel := range def.Relations {
			b := subjectBase{definition: def.Name, relation: rel.Name}
			if !bases[b] {
				continue
			}
			subRefRelations[rel.Name] = true
			dd.SubRefs = append(dd.SubRefs, DefSubRefData{PropName: escapeKeyword(rel.Name), ReturnType: b.className()})
		}
		for _, perm := range def.Permissions {
			b := subjectBase{definition: def.Name, relation: perm.Name}
			if !bases[b] {
				continue
			}
			dd.SubRefs = append(dd.SubRefs, DefSubRefData{PropName: perm.Name + "_subject", ReturnType: b.className()})
		}
		for _, sr := range dd.SubRefs {
			n.claim(pascal, sr.PropName, "subject accessor returning "+sr.ReturnType)
		}

		for _, perm := range def.Permissions {
			pp := toPascalCase(perm.Name)
			cls := "_" + pascal + pp + "Permission"
			ref := "_" + pascal + pp + "Ref"
			n.claim("", cls, "permission class for "+def.Name+"#"+perm.Name)
			n.claim("", ref, "permission ref class for "+def.Name+"#"+perm.Name)
			members, lookup := checkMembers(perm.ReachableSubjects)
			union := addUnion(pascal+pp+"Subject", "subjects of permission "+def.Name+"#"+perm.Name, members)
			lookupTypes := addUnion(pascal+pp+"SubjectType", "lookup types of permission "+def.Name+"#"+perm.Name, typeArgs(lookup))
			checkables = append(checkables, CheckableData{
				ClassName: cls, RefClassName: ref, SubjectUnion: union, LookupTypes: lookupTypes,
				Describe: def.Name + "#" + perm.Name,
			})
			pd := DefPermissionData{PropName: escapeKeyword(perm.Name), ClassName: cls, Name: perm.Name, ConstName: strings.ToUpper(perm.Name)}
			n.claim(pascal, pd.PropName, "permission "+perm.Name)
			dd.Permissions = append(dd.Permissions, pd)
			dd.PermissionNameList = append(dd.PermissionNameList, perm.Name)

			constName := upper + "_" + strings.ToUpper(perm.Name)
			n.claim("", constName, "permission ref constant for "+def.Name+"#"+perm.Name)
			staticPermRefs = append(staticPermRefs, StaticPermRefData{
				ConstName: constName, RefClassName: ref, ResourceType: def.Name, Permission: perm.Name,
			})
		}

		for _, rel := range def.Relations {
			rp := toPascalCase(rel.Name)
			rd := DefRelationData{
				Name:       rel.Name,
				ConstName:  strings.ToUpper(rel.Name),
				WriterName: escapeKeyword(rel.Name),
				KeyName:    rel.Name + "_key",
				CheckName:  rel.Name + "_relation",
				CheckClass: "_" + pascal + rp + "Relation",
				FilterName: rel.Name + "_filter",
			}
			if subRefRelations[rel.Name] {
				rd.WriterName = "for_" + rel.Name
			}
			rd.SubjectUnion = addUnion(pascal+rp+"Subject", "subjects of relation "+def.Name+"#"+rel.Name, exactMembers(rel.AllowedSubjects))
			rd.KeyUnion = addUnion(pascal+rp+"KeySubject", "key subjects of relation "+def.Name+"#"+rel.Name, keyMembers(rel.AllowedSubjects))
			rd.HasSubjects = rd.SubjectUnion != ""
			rd.HasKeySubject = rd.KeyUnion != ""
			if rd.HasSubjects {
				n.claim(pascal, rd.WriterName, "writer for relation "+rel.Name)
			}
			if rd.HasKeySubject {
				n.claim(pascal, rd.KeyName, "key for relation "+rel.Name)
			}
			n.claim(pascal, rd.CheckName, "check accessor for relation "+rel.Name)
			n.claim(pascal, rd.FilterName, "filter for relation "+rel.Name)

			n.claim("", rd.CheckClass, "relation check class for "+def.Name+"#"+rel.Name)
			members, lookup := checkMembers(rel.AllowedSubjects)
			union := addUnion(pascal+rp+"RelationSubject", "check subjects of relation "+def.Name+"#"+rel.Name, members)
			lookupTypes := addUnion(pascal+rp+"RelationSubjectType", "lookup types of relation "+def.Name+"#"+rel.Name, typeArgs(lookup))
			checkables = append(checkables, CheckableData{
				ClassName: rd.CheckClass, SubjectUnion: union, LookupTypes: lookupTypes,
				Describe: def.Name + "#" + rel.Name,
			})
			dd.Relations = append(dd.Relations, rd)
			dd.RelationNameList = append(dd.RelationNameList, rel.Name)
		}
		definitions = append(definitions, dd)
	}
	if n.err != nil {
		return TemplateData{}, n.err
	}

	data := TemplateData{
		CaveatContexts: caveatContexts,
		Definitions:    definitions,
		SubjectRefs:    subjectRefs,
		SealedUnions:   sealedUnions,
		Checkables:     checkables,
		StaticPermRefs: staticPermRefs,
	}
	data.ClientTypeImports = clientTypeImports(data)
	return data, nil
}

// clientTypeImports returns the sorted, deduplicated types-module names that
// a TypedClient body (sync.py/aio.py) references.
func clientTypeImports(data TemplateData) []string {
	set := map[string]bool{
		"Permission":        true,
		"RelationshipKey":   true,
		"Subject":           true,
		"TypedRelationship": true,
		"TypedTransaction":  true,
	}
	for _, c := range data.Checkables {
		if c.SubjectUnion == "" {
			continue
		}
		set[c.ClassName] = true
		set[c.SubjectUnion] = true
		if c.RefClassName != "" {
			set[c.RefClassName] = true
		}
		if c.LookupTypes != "" {
			set[c.LookupTypes] = true
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
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

// pyHardKeywords are Python's reserved words; an identifier may never be one.
var pyHardKeywords = map[string]bool{
	"False": true, "None": true, "True": true,
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

// pyKeywords are the reserved words plus soft keywords and builtins that
// generated members should not shadow. Hits get a trailing underscore.
var pyKeywords = func() map[string]bool {
	m := map[string]bool{
		"match": true, "case": true, "type": true,
		"id": true, "list": true, "dict": true, "set": true,
		"str": true, "int": true, "float": true, "bool": true, "bytes": true,
	}
	for k := range pyHardKeywords {
		m[k] = true
	}
	return m
}()

// escapeKeyword appends "_" if name would shadow a Python keyword or common builtin.
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
