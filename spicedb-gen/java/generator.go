package java

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

// Generator implements generator.LanguageGenerator for Java.
type Generator struct{}

// Language returns "java".
func (g *Generator) Language() string {
	return "java"
}

// renameMu serializes Generate, which installs per-call definition renames for defIdent.
var (
	renameMu   sync.Mutex
	defRenames map[string]string
)

// defIdent is the Java identifier stem for a schema definition: its PascalCase name, or the
// --java.rename override for it.
func defIdent(name string) string {
	if r, ok := defRenames[name]; ok {
		return r
	}
	return toPascalCase(name)
}

var javaTypeIdent = regexp.MustCompile(`^[A-Z][A-Za-z0-9_$]*$`)

// parseRenames parses --java.rename=def=JavaName[,def=JavaName...].
func parseRenames(v string) (map[string]string, error) {
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		def, ident, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || def == "" || !javaTypeIdent.MatchString(ident) || javaReserved[ident] {
			return nil, fmt.Errorf("--java.rename entry %q must be <definition>=<JavaName> (an identifier starting with an upper-case letter)", pair)
		}
		out[def] = ident
	}
	return out, nil
}

// Generate produces generated Java files from the parsed schema.
//
// Options:
//   - "package" (required): the Java package of the generated Permissions class.
//   - "rename" (optional): --java.rename=def=JavaName[,def=JavaName...] sets the Java identifier
//     stem for definitions whose names would otherwise collide with generated or native
//     identifiers (e.g. "subject=SubjectDef" for a schema with `definition subject`). Renames
//     change Java identifiers only; the schema names sent to SpiceDB never change.
//   - "formatter" (optional): a command that reads Java source on stdin and writes the
//     formatted source to stdout, e.g. "google-java-format -". The template already emits
//     google-java-format output for ordinary schema names; the formatter is the escape hatch for
//     names long enough to push a line past google-java-format's 100-column limit.
func (g *Generator) Generate(s *schema.Schema, opts map[string]string) ([]generator.GeneratedFile, error) {
	pkg, ok := opts["package"]
	if !ok || pkg == "" {
		return nil, fmt.Errorf("java generator requires --java.package=<package> option")
	}

	renames := map[string]string{}
	for k, v := range opts {
		switch k {
		case "package", "formatter":
		case "rename":
			r, err := parseRenames(v)
			if err != nil {
				return nil, err
			}
			renames = r
		default:
			return nil, fmt.Errorf("unknown Java option --java.%s", k)
		}
	}
	known := map[string]bool{}
	for _, def := range s.Definitions {
		known[def.Name] = true
	}
	for def := range renames {
		if !known[def] {
			return nil, fmt.Errorf("--java.rename names %q, which is not a definition in the schema", def)
		}
	}

	renameMu.Lock()
	defer renameMu.Unlock()
	defRenames = renames
	defer func() { defRenames = nil }()

	data, err := buildTemplateData(s, pkg)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New("typed_client.java.tmpl").ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	content := buf.Bytes()

	if formatter := strings.TrimSpace(opts["formatter"]); formatter != "" {
		content, err = runFormatter(formatter, content)
		if err != nil {
			return nil, err
		}
	}

	return []generator.GeneratedFile{
		{
			Path:    "Permissions.java",
			Content: content,
		},
	}, nil
}

func runFormatter(command string, source []byte) ([]byte, error) {
	args := strings.Fields(command)
	cmd := exec.Command(args[0], args[1:]...) // #nosec G204 -- the caller names the formatter
	cmd.Stdin = bytes.NewReader(source)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("java formatter %q failed: %w: %s", command, err, stderr.String())
	}
	return out, nil
}

// TemplateData holds all pre-computed data needed by the Java template. Schema-dependent code is
// rendered here into google-java-format-shaped blocks, because wrapping depends on line lengths.
type TemplateData struct {
	Package string
	// CaveatContexts, Interfaces, Subjects, Sentinels and Definitions are pre-rendered member
	// blocks (each already indented for a member of the Permissions class).
	CaveatContexts []string
	Interfaces     []string
	Subjects       []string
	Sentinels      []string
	Definitions    []string
	Statics        []string

	// Inventory used by tests.
	SubjectTypes   []string
	SentinelNames  []string
	InterfaceNames []string
	Defs           []DefinitionData
}

// DefinitionData summarises the generated API for one definition (for tests and docs).
type DefinitionData struct {
	Name          string
	RefType       string
	Writers       map[string]string // relation -> writer method
	SubRefMethods map[string]string // accessor -> returned ref type
}

// variantKey identifies one subject shape a schema position can name.
type variantKey struct {
	Def      string
	Rel      string
	Wildcard bool
	Caveat   string
	Expiring bool
}

func (k variantKey) base() variantKey {
	return variantKey{Def: k.Def, Rel: k.Rel, Wildcard: k.Wildcard}
}

func keyOf(st schema.SubjectType) variantKey {
	return variantKey{Def: st.Definition, Rel: st.Relation, Wildcard: st.Wildcard, Caveat: st.CaveatName, Expiring: st.Expiration}
}

// checkKeyOf is the subject shape a CHECK admits for an allowed/reachable subject: expiration is a
// write-time property, and a wildcard grants every concrete object of the type.
func checkKeyOf(st schema.SubjectType) variantKey {
	if st.Wildcard {
		return variantKey{Def: st.Definition}
	}
	return variantKey{Def: st.Definition, Rel: st.Relation, Caveat: st.CaveatName}
}

func prefixOf(k variantKey) string {
	p := defIdent(k.Def)
	switch {
	case k.Rel != "":
		p += toPascalCase(k.Rel)
	case k.Wildcard:
		p += "Wildcard"
	}
	return p
}

func typeNameOf(k variantKey) string {
	if k.Caveat == "" && !k.Expiring {
		return prefixOf(k) + "Ref"
	}
	name := prefixOf(k) + toPascalCase(k.Caveat)
	if k.Expiring {
		name += "Expiring"
	}
	return name
}

func sentinelNameOf(k variantKey) string {
	if k.Rel != "" {
		return defIdent(k.Def) + toPascalCase(k.Rel) + "Type"
	}
	return defIdent(k.Def) + "Type"
}

type transition struct {
	method string
	param  string // full parameter declaration
	target variantKey
	arg    string
}

type variant struct {
	key         variantKey
	typeName    string
	interfaces  []string
	transitions []transition
}

type sealedIface struct {
	name    string
	permits []string
}

// registry detects identifier collisions. Java keeps types, methods and fields in separate
// namespaces, and each generated class has its own member namespace.
type registry struct {
	owners map[string]string
}

func newRegistry() *registry { return &registry{owners: map[string]string{}} }

func (r *registry) claim(namespace, name, source string) error {
	if javaReserved[name] {
		return fmt.Errorf("java: %s would generate the identifier %q, which is a Java reserved word", source, name)
	}
	key := namespace + "\x00" + name
	if prev, ok := r.owners[key]; ok {
		if prev == source {
			return nil
		}
		return fmt.Errorf("java: generated identifier %q (%s) collides: %s and %s both produce it; "+
			"rename one in the schema, or keep the schema and pass --java.rename=<definition>=<JavaName> to change a definition's Java identifiers",
			name, namespace, prev, source)
	}
	r.owners[key] = source
	return nil
}

var javaReserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`abstract assert boolean break byte case catch char class const continue
default do double else enum extends final finally float for goto if implements import instanceof int
interface long native new package private protected public return short static strictfp super switch
synchronized this throw throws transient try void volatile while true false null var yield record
sealed permits non-sealed when`) {
		javaReserved[w] = true
	}
}

// fixedTypes are the hand-written nested types in the template and the native types it references
// by simple name; a generated type with one of these names would shadow them.
var fixedTypes = []string{
	"Subject", "Permission", "PermissionRef", "TypedCheck", "TypedRelationship", "RelationshipKey",
	"Deletable", "Txn", "TypedClient", "NotInSchemaException", "SubjectNotAllowedException",
	// Native types referenced by simple name.
	"CallOptions", "CheckOptions", "CheckResult", "ClientOptions", "Consistency", "DeleteAllOptions",
	"DeleteOptions", "ExportOptions", "Filter", "ImportOptions", "LookupOptions", "LookupResult",
	"Materialize", "ObjectRef", "PermissionCheck", "PermissionSetOptions", "ReadOptions",
	"Relationship", "ReadRelationship", "ExportedRelationship", "PermissionsApi", "SpiceDBClient", "SubjectRef", "SubjectRelationFilter", "Transaction",
	"WatchOptions", "WriteOptions", "WriteResult", "ZedToken", "DeleteResult", "SchemaResult",
	"ReflectSchemaResult", "ComputablePermissionsResult", "DependentRelationsResult",
	"DiffSchemaResult", "ExpandResult", "WatchEvent", "CountResult", "Instant", "List", "Map",
	"Objects", "Optional", "Stream", "Collection", "ArrayList", "HashMap", "String", "Object",
}

var fixedStaticMethods = []string{"withSubject", "withSubjectAnyRelation", "checksFor", "subjectRef", "subjectString", "resourceFilter", "notAllowed"}

// refMembers are the members every definition ref record declares besides schema-derived ones.
var refMembers = []string{
	"id", "toString", "equals", "hashCode", "getClass", "notify", "notifyAll", "wait", "filter",
	"permissionByName", "relationByName", "relationshipByName", "definitionName", "subRelation",
	"caveatName", "caveatContext", "expiration",
}

type builder struct {
	s        *schema.Schema
	defs     map[string]schema.Definition
	variants map[variantKey]*variant
	order    []variantKey
	reg      *registry
}

// ensure creates the variant for k and the chain of variants that construct it.
func (b *builder) ensure(k variantKey) {
	if _, ok := b.variants[k]; ok {
		return
	}
	v := &variant{key: k, typeName: typeNameOf(k)}
	b.variants[k] = v
	b.order = append(b.order, k)
	base := k.base()
	switch {
	case k.Caveat != "" && k.Expiring:
		from := variantKey{Def: k.Def, Rel: k.Rel, Wildcard: k.Wildcard, Caveat: k.Caveat}
		b.ensure(from)
		b.addTransition(from, transition{method: "withExpiration", param: "Instant expiration", target: k, arg: "expiration"})
	case k.Expiring:
		b.ensure(base)
		b.addTransition(base, transition{method: "withExpiration", param: "Instant expiration", target: k, arg: "expiration"})
	case k.Caveat != "":
		b.ensure(base)
		b.addTransition(base, transition{
			method: "with" + toPascalCase(k.Caveat),
			param:  toPascalCase(k.Caveat) + "Context ctx",
			target: k, arg: "ctx",
		})
	}
}

func (b *builder) addTransition(from variantKey, t transition) {
	v := b.variants[from]
	for _, existing := range v.transitions {
		if existing.target == t.target {
			return
		}
	}
	v.transitions = append(v.transitions, t)
}

// subjectsOf returns the allowed subjects of a relation or the reachable subjects of a permission.
func (b *builder) subjectsOf(def, name string) ([]schema.SubjectType, bool) {
	d, ok := b.defs[def]
	if !ok {
		return nil, false
	}
	for _, r := range d.Relations {
		if r.Name == name {
			return r.AllowedSubjects, true
		}
	}
	for _, p := range d.Permissions {
		if p.Name == name {
			return p.ReachableSubjects, true
		}
	}
	return nil, false
}

// checkKeys returns the subject shapes a check of (def, name) admits, following userset subjects
// to the subjects that are members of them.
func (b *builder) checkKeys(subjects []schema.SubjectType, visited map[string]bool) []variantKey {
	var out []variantKey
	for _, st := range subjects {
		out = append(out, checkKeyOf(st))
		if st.Relation == "" {
			continue
		}
		id := st.Definition + "#" + st.Relation
		if visited[id] {
			continue
		}
		visited[id] = true
		if nested, ok := b.subjectsOf(st.Definition, st.Relation); ok {
			out = append(out, b.checkKeys(nested, visited)...)
		}
	}
	return out
}

func usersetSubRefs(s *schema.Schema) map[variantKey]bool {
	used := map[variantKey]bool{}
	for _, def := range s.Definitions {
		for _, rel := range def.Relations {
			for _, st := range rel.AllowedSubjects {
				if st.Relation != "" {
					used[variantKey{Def: st.Definition, Rel: st.Relation}] = true
				}
			}
		}
		for _, perm := range def.Permissions {
			for _, st := range perm.ReachableSubjects {
				if st.Relation != "" {
					used[variantKey{Def: st.Definition, Rel: st.Relation}] = true
				}
			}
		}
	}
	return used
}

type relInfo struct {
	name, writer, writerIface, keyIface, checkIface string
	// needsKey is set when some admitted subject carries a caveat or expiration; otherwise the
	// writer's TypedRelationship already identifies the tuple, and a key would duplicate it.
	needsKey bool
}

type permInfo struct {
	name, method, iface string
}

// buildTemplateData pre-computes and renders all schema-dependent code.
func buildTemplateData(s *schema.Schema, pkg string) (TemplateData, error) {
	b := &builder{
		s:        s,
		defs:     map[string]schema.Definition{},
		variants: map[variantKey]*variant{},
		reg:      newRegistry(),
	}
	for _, d := range s.Definitions {
		b.defs[d.Name] = d
	}
	for _, t := range fixedTypes {
		if err := b.reg.claim("type", t, "the generated runtime ("+t+")"); err != nil {
			return TemplateData{}, err
		}
	}
	for _, m := range fixedStaticMethods {
		if err := b.reg.claim("static method", m, "the generated runtime ("+m+")"); err != nil {
			return TemplateData{}, err
		}
	}

	subRefs := usersetSubRefs(s)

	// Every definition gets a ref record; usersets and subject shapes get theirs.
	for _, d := range s.Definitions {
		b.ensure(variantKey{Def: d.Name})
	}
	for _, d := range s.Definitions {
		for _, r := range d.Relations {
			for _, st := range r.AllowedSubjects {
				b.ensure(keyOf(st))
			}
		}
	}
	for k := range subRefs {
		b.ensure(k)
	}

	var ifaces []sealedIface
	sentinelUse := map[variantKey]bool{}
	ifaceSources := map[string]string{}
	addIface := func(name, source string, permits []string) error {
		if err := b.reg.claim("type", name, source); err != nil {
			return err
		}
		ifaceSources[name] = source
		ifaces = append(ifaces, sealedIface{name: name, permits: dedupSorted(permits)})
		return nil
	}
	checkPermits := func(keys []variantKey) []string {
		var permits []string
		for _, k := range keys {
			b.ensure(k)
			permits = append(permits, typeNameOf(k))
			sk := variantKey{Def: k.Def, Rel: k.Rel}
			sentinelUse[sk] = true
			permits = append(permits, sentinelNameOf(sk))
		}
		return permits
	}

	defRels := map[string][]relInfo{}
	defPerms := map[string][]permInfo{}
	for _, d := range s.Definitions {
		dp := defIdent(d.Name)
		for _, r := range d.Relations {
			rp := toPascalCase(r.Name)
			info := relInfo{
				name:        r.Name,
				writer:      toLowerCamel(r.Name),
				writerIface: dp + rp + "Subject",
				keyIface:    dp + rp + "KeySubject",
				checkIface:  dp + rp + "RelationSubject",
			}
			if subRefs[variantKey{Def: d.Name, Rel: r.Name}] {
				info.writer = "for" + rp
			}
			var writerPermits, keyPermits []string
			for _, st := range r.AllowedSubjects {
				writerPermits = append(writerPermits, typeNameOf(keyOf(st)))
				keyPermits = append(keyPermits, typeNameOf(keyOf(st).base()))
				if st.CaveatName != "" || st.Expiration {
					info.needsKey = true
				}
			}
			src := fmt.Sprintf("definition %q relation %q", d.Name, r.Name)
			if err := addIface(info.writerIface, src+" (writer subjects)", writerPermits); err != nil {
				return TemplateData{}, err
			}
			if info.needsKey {
				if err := addIface(info.keyIface, src+" (key subjects)", keyPermits); err != nil {
					return TemplateData{}, err
				}
			}
			checks := checkPermits(b.checkKeys(r.AllowedSubjects, map[string]bool{d.Name + "#" + r.Name: true}))
			if err := addIface(info.checkIface, src+" (relation check subjects)", checks); err != nil {
				return TemplateData{}, err
			}
			defRels[d.Name] = append(defRels[d.Name], info)
		}
		for _, p := range d.Permissions {
			info := permInfo{name: p.Name, method: toLowerCamel(p.Name), iface: dp + toPascalCase(p.Name) + "Subject"}
			checks := checkPermits(b.checkKeys(p.ReachableSubjects, map[string]bool{d.Name + "#" + p.Name: true}))
			if err := addIface(info.iface, fmt.Sprintf("definition %q permission %q (check subjects)", d.Name, p.Name), checks); err != nil {
				return TemplateData{}, err
			}
			defPerms[d.Name] = append(defPerms[d.Name], info)
		}
	}

	// Concrete type -> interfaces it implements.
	implements := map[string][]string{}
	inIface := map[string]bool{}
	for _, si := range ifaces {
		for _, p := range si.permits {
			implements[p] = append(implements[p], si.name)
			inIface[p] = true
		}
	}

	// Sentinels.
	var sentinelKeys []variantKey
	for k := range sentinelUse {
		sentinelKeys = append(sentinelKeys, k)
	}
	sort.Slice(sentinelKeys, func(i, j int) bool { return sentinelNameOf(sentinelKeys[i]) < sentinelNameOf(sentinelKeys[j]) })

	// Claim variant type names (one variant per name).
	for _, k := range b.order {
		v := b.variants[k]
		src := describeVariant(k)
		if err := b.reg.claim("type", v.typeName, src); err != nil {
			return TemplateData{}, err
		}
		v.interfaces = implements[v.typeName]
	}
	for _, k := range sentinelKeys {
		name := sentinelNameOf(k)
		src := "type sentinel for " + describeVariant(k)
		if err := b.reg.claim("type", name, src); err != nil {
			return TemplateData{}, err
		}
		if err := b.reg.claim("static field", name, src); err != nil {
			return TemplateData{}, err
		}
	}

	// Subject's direct permits: every sub-interface plus concrete types in none.
	var subjectPermits []string
	for _, si := range ifaces {
		subjectPermits = append(subjectPermits, si.name)
	}
	for _, k := range b.order {
		if !inIface[typeNameOf(k)] {
			subjectPermits = append(subjectPermits, typeNameOf(k))
		}
	}
	for _, k := range sentinelKeys {
		if !inIface[sentinelNameOf(k)] {
			subjectPermits = append(subjectPermits, sentinelNameOf(k))
		}
	}

	data := TemplateData{Package: pkg}

	// Caveat contexts.
	usedCaveats := map[string]bool{}
	for k := range b.variants {
		if k.Caveat != "" {
			usedCaveats[k.Caveat] = true
		}
	}
	for _, c := range s.Caveats {
		if !usedCaveats[c.Name] {
			continue
		}
		block, err := renderCaveatContext(b.reg, c)
		if err != nil {
			return TemplateData{}, err
		}
		data.CaveatContexts = append(data.CaveatContexts, block)
	}

	// Interfaces: Subject first.
	{
		w := newWriter(2)
		w.header("public sealed interface Subject", "permits", dedupSorted(subjectPermits), " {")
		w.indent()
		for i, m := range []string{"String definitionName();", "String id();", "String subRelation();", "String caveatName();", "Map<String, Object> caveatContext();", "Instant expiration();"} {
			if i > 0 {
				w.blank()
			}
			w.line(m)
		}
		w.dedent()
		w.line("}")
		data.Interfaces = append(data.Interfaces, w.String())
	}
	sort.Slice(ifaces, func(i, j int) bool { return ifaces[i].name < ifaces[j].name })
	for _, si := range ifaces {
		w := newWriter(2)
		if len(si.permits) == 0 {
			w.line("public non-sealed interface " + si.name + " extends Subject {}")
		} else {
			w.header("public sealed interface "+si.name+" extends Subject", "permits", si.permits, " {}")
		}
		data.Interfaces = append(data.Interfaces, w.String())
		data.InterfaceNames = append(data.InterfaceNames, si.name)
	}

	// Subject variant records (definition refs are rendered with their definition).
	for _, k := range b.order {
		if k.Rel == "" && !k.Wildcard && k.Caveat == "" && !k.Expiring {
			continue
		}
		block, err := b.renderVariant(b.variants[k])
		if err != nil {
			return TemplateData{}, err
		}
		data.Subjects = append(data.Subjects, block)
		data.SubjectTypes = append(data.SubjectTypes, b.variants[k].typeName)
	}

	// Sentinels.
	for _, k := range sentinelKeys {
		data.Sentinels = append(data.Sentinels, renderSentinel(k, implements[sentinelNameOf(k)]))
		data.SentinelNames = append(data.SentinelNames, sentinelNameOf(k))
	}

	// Definitions.
	for _, d := range s.Definitions {
		ref, statics, dd, err := b.renderDefinition(d, defRels[d.Name], defPerms[d.Name], subRefs)
		if err != nil {
			return TemplateData{}, err
		}
		data.Definitions = append(data.Definitions, ref)
		data.Statics = append(data.Statics, statics...)
		data.Defs = append(data.Defs, dd)
		data.SubjectTypes = append(data.SubjectTypes, dd.RefType)
	}

	_ = ifaceSources
	return data, nil
}

func describeVariant(k variantKey) string {
	var b strings.Builder
	fmt.Fprintf(&b, "subject %q", k.Def)
	if k.Rel != "" {
		b.WriteString("#" + k.Rel)
	}
	if k.Wildcard {
		b.WriteString(":*")
	}
	if k.Caveat != "" {
		b.WriteString(" with " + k.Caveat)
	}
	if k.Expiring {
		b.WriteString(" with expiration")
	}
	return b.String()
}

func renderCaveatContext(reg *registry, c schema.CaveatDefinition) (string, error) {
	class := toPascalCase(c.Name) + "Context"
	if err := reg.claim("type", class, fmt.Sprintf("caveat %q (context class)", c.Name)); err != nil {
		return "", err
	}
	local := newRegistry()
	_ = local.claim("member", "toMap", "the generated toMap method")
	w := newWriter(2)
	w.line("public static final class " + class + " {")
	w.indent()
	for _, p := range c.Params {
		field := toLowerCamel(p.Name)
		if err := local.claim("field", field, fmt.Sprintf("caveat %q parameter %q", c.Name, p.Name)); err != nil {
			return "", err
		}
		w.line("private " + celTypeToJavaType(p.Type) + " " + field + ";")
		w.blank()
	}
	for _, p := range c.Params {
		field := toLowerCamel(p.Name)
		method := "with" + toPascalCase(p.Name)
		if err := local.claim("member", method, fmt.Sprintf("caveat %q parameter %q", c.Name, p.Name)); err != nil {
			return "", err
		}
		w.line("public " + class + " " + method + "(" + celTypeToJavaType(p.Type) + " v) {")
		w.indent()
		w.line("this." + field + " = v;")
		w.line("return this;")
		w.dedent()
		w.line("}")
		w.blank()
	}
	w.line("public Map<String, Object> toMap() {")
	w.indent()
	w.line("var m = new HashMap<String, Object>();")
	for _, p := range c.Params {
		field := toLowerCamel(p.Name)
		w.line("if (" + field + " != null) {")
		w.indent()
		w.line("m.put(" + javaString(p.Name) + ", " + field + ");")
		w.dedent()
		w.line("}")
	}
	w.line("return m;")
	w.dedent()
	w.line("}")
	w.dedent()
	w.line("}")
	return w.String(), nil
}

// components returns the record components of a variant.
func components(k variantKey) []string {
	var c []string
	if !k.Wildcard {
		c = append(c, "String id")
	}
	if k.Caveat != "" {
		c = append(c, toPascalCase(k.Caveat)+"Context context")
	}
	if k.Expiring {
		c = append(c, "Instant expiration")
	}
	return c
}

// ctorArgs returns the constructor arguments to build target from a value of variant from, with
// the transition argument appended.
func ctorArgs(target variantKey, arg string) []string {
	var a []string
	if !target.Wildcard {
		a = append(a, "id")
	}
	if target.Caveat != "" {
		if strings.HasSuffix(arg, "ctx") {
			a = append(a, arg)
		} else {
			a = append(a, "context")
		}
	}
	if target.Expiring {
		a = append(a, "expiration")
	}
	return a
}

func (b *builder) subjectMethods(w *writer, k variantKey) {
	method := func(sig, body string) {
		w.line("@Override")
		w.line("public " + sig + " {")
		w.indent()
		w.line(body)
		w.dedent()
		w.line("}")
		w.blank()
	}
	method("String definitionName()", "return "+javaString(k.Def)+";")
	if k.Wildcard {
		method("String id()", `return "*";`)
	}
	method("String subRelation()", "return "+javaString(k.Rel)+";")
	method("String caveatName()", "return "+javaString(k.Caveat)+";")
	if k.Caveat != "" {
		method("Map<String, Object> caveatContext()", "return context.toMap();")
	} else {
		method("Map<String, Object> caveatContext()", "return Map.of();")
	}
	if !k.Expiring {
		method("Instant expiration()", "return null;")
	}
}

func (b *builder) transitions(w *writer, v *variant, members *registry, owner string) error {
	for _, t := range v.transitions {
		if err := members.claim("member", t.method, owner+" (subject variant "+typeNameOf(t.target)+")"); err != nil {
			return err
		}
		w.line("public " + typeNameOf(t.target) + " " + t.method + "(" + t.param + ") {")
		w.indent()
		w.call("return new "+typeNameOf(t.target)+"(", ctorArgs(t.target, t.arg), ");")
		w.dedent()
		w.line("}")
		w.blank()
	}
	return nil
}

func (b *builder) renderVariant(v *variant) (string, error) {
	k := v.key
	w := newWriter(2)
	ifaces := v.interfaces
	if len(ifaces) == 0 {
		ifaces = []string{"Subject"}
	}
	w.header("public record "+v.typeName+"("+strings.Join(components(k), ", ")+")", "implements", ifaces, " {")
	w.indent()
	b.subjectMethods(w, k)
	members := newRegistry()
	for _, m := range refMembers {
		_ = members.claim("member", m, "the generated record member "+m)
	}
	if err := b.transitions(w, v, members, describeVariant(k)); err != nil {
		return "", err
	}
	w.line("@Override")
	w.line("public String toString() {")
	w.indent()
	w.line("return subjectString(this);")
	w.dedent()
	w.line("}")
	w.dedent()
	w.line("}")
	return w.String(), nil
}

func renderSentinel(k variantKey, ifaces []string) string {
	name := sentinelNameOf(k)
	w := newWriter(2)
	w.field("public static final "+name+" "+name, "new "+name+"()")
	w.blank()
	w.header("public static final class "+name, "implements", ifaces, " {")
	w.indent()
	w.line("private " + name + "() {}")
	w.blank()
	method := func(sig, body string) {
		w.line("@Override")
		w.line("public " + sig + " {")
		w.indent()
		w.line(body)
		w.dedent()
		w.line("}")
	}
	method("String definitionName()", "return "+javaString(k.Def)+";")
	w.blank()
	method("String id()", `return "";`)
	w.blank()
	method("String subRelation()", "return "+javaString(k.Rel)+";")
	w.blank()
	method("String caveatName()", `return "";`)
	w.blank()
	method("Map<String, Object> caveatContext()", "return Map.of();")
	w.blank()
	method("Instant expiration()", "return null;")
	w.blank()
	method("String toString()", "return subjectString(this);")
	w.dedent()
	w.line("}")
	return w.String()
}

func (b *builder) renderDefinition(d schema.Definition, rels []relInfo, perms []permInfo, subRefs map[variantKey]bool) (string, []string, DefinitionData, error) {
	dp := defIdent(d.Name)
	refKey := variantKey{Def: d.Name}
	v := b.variants[refKey]
	dd := DefinitionData{Name: d.Name, RefType: v.typeName, Writers: map[string]string{}, SubRefMethods: map[string]string{}}
	members := newRegistry()
	for _, m := range refMembers {
		_ = members.claim("member", m, "the generated record member "+m+"()")
	}
	src := func(what string) string { return fmt.Sprintf("definition %q %s", d.Name, what) }

	w := newWriter(2)
	ifaces := v.interfaces
	if len(ifaces) == 0 {
		ifaces = []string{"Subject"}
	}
	w.header("public record "+v.typeName+"(String id)", "implements", ifaces, " {")
	w.indent()
	b.subjectMethods(w, refKey)
	if err := b.transitions(w, v, members, src("subject")); err != nil {
		return "", nil, dd, err
	}
	w.line("@Override")
	w.line("public String toString() {")
	w.indent()
	w.line("return " + javaString(d.Name+":") + " + id;")
	w.dedent()
	w.line("}")
	w.blank()
	w.line("/** The exact filter for this object. An empty ID is rejected rather than widened. */")
	w.line("public Filter filter() {")
	w.indent()
	w.call("return resourceFilter(", []string{javaString(d.Name), "id", `""`}, ");")
	w.dedent()
	w.line("}")

	for _, p := range perms {
		if err := members.claim("member", p.method, src(fmt.Sprintf("permission %q", p.name))); err != nil {
			return "", nil, dd, err
		}
		w.blank()
		w.line("public Permission<" + p.iface + "> " + p.method + "() {")
		w.indent()
		w.call("return new Permission<>(", []string{javaString(d.Name), "id", javaString(p.name), p.iface + ".class"}, ");")
		w.dedent()
		w.line("}")
		if sk := (variantKey{Def: d.Name, Rel: p.name}); subRefs[sk] {
			acc := p.method + "Subject"
			if err := members.claim("member", acc, src(fmt.Sprintf("permission userset %s#%s (subject accessor)", d.Name, p.name))); err != nil {
				return "", nil, dd, err
			}
			dd.SubRefMethods[acc] = typeNameOf(sk)
			w.blank()
			w.line("public " + typeNameOf(sk) + " " + acc + "() {")
			w.indent()
			w.line("return new " + typeNameOf(sk) + "(id);")
			w.dedent()
			w.line("}")
		}
	}

	for _, r := range rels {
		rc := toLowerCamel(r.name)
		if sk := (variantKey{Def: d.Name, Rel: r.name}); subRefs[sk] {
			if err := members.claim("member", rc, src(fmt.Sprintf("relation userset %s#%s (subject accessor)", d.Name, r.name))); err != nil {
				return "", nil, dd, err
			}
			dd.SubRefMethods[rc] = typeNameOf(sk)
			w.blank()
			w.line("public " + typeNameOf(sk) + " " + rc + "() {")
			w.indent()
			w.line("return new " + typeNameOf(sk) + "(id);")
			w.dedent()
			w.line("}")
		}
		claims := []struct{ name, what string }{
			{r.writer, "writer"}, {rc + "Relation", "relation check"}, {rc + "Filter", "filter"},
		}
		if r.needsKey {
			claims = append(claims, struct{ name, what string }{rc + "Key", "key"})
		}
		for _, m := range claims {
			if err := members.claim("member", m.name, src(fmt.Sprintf("relation %q (%s)", r.name, m.what))); err != nil {
				return "", nil, dd, err
			}
		}
		dd.Writers[r.name] = r.writer
		args := []string{javaString(d.Name), "id", javaString(r.name), "subject"}
		w.blank()
		w.line("public TypedRelationship " + r.writer + "(" + r.writerIface + " subject) {")
		w.indent()
		w.call("return TypedRelationship.of(", args, ");")
		w.dedent()
		w.line("}")
		if r.needsKey {
			w.blank()
			w.line("/** Identifies a relationship by tuple alone, for deletes and exact filters. */")
			w.line("public RelationshipKey " + rc + "Key(" + r.keyIface + " subject) {")
			w.indent()
			w.call("return RelationshipKey.of(", args, ");")
			w.dedent()
			w.line("}")
		}
		w.blank()
		w.line("/** Checks the relation itself (not a permission). */")
		w.line("public Permission<" + r.checkIface + "> " + rc + "Relation() {")
		w.indent()
		w.call("return new Permission<>(", []string{javaString(d.Name), "id", javaString(r.name), r.checkIface + ".class"}, ");")
		w.dedent()
		w.line("}")
		w.blank()
		w.line("public Filter " + rc + "Filter() {")
		w.indent()
		w.call("return resourceFilter(", []string{javaString(d.Name), "id", javaString(r.name)}, ");")
		w.dedent()
		w.line("}")
	}

	// By-name lookups.
	w.blank()
	w.line("/** Resolves a permission name validated against the schema. */")
	w.line("public Permission<?> permissionByName(String name) {")
	w.indent()
	byName := func(cases [][2]string, what string) {
		if len(cases) > 0 {
			w.line("switch (name) {")
			w.indent()
			for _, c := range cases {
				w.line("case " + javaString(c[0]) + ":")
				w.indent()
				w.line("return " + c[1] + "();")
				w.dedent()
			}
			w.line("default:")
			w.indent()
		}
		w.call("throw new NotInSchemaException(", []string{javaString(d.Name+" has no "+what+" ") + " + name"}, ");")
		if len(cases) > 0 {
			w.dedent()
			w.dedent()
			w.line("}")
		}
	}
	var permCases, relCases [][2]string
	for _, p := range perms {
		permCases = append(permCases, [2]string{p.name, p.method})
	}
	for _, r := range rels {
		relCases = append(relCases, [2]string{r.name, toLowerCamel(r.name) + "Relation"})
	}
	byName(permCases, "permission")
	w.dedent()
	w.line("}")
	w.blank()
	w.line("/** Resolves a relation name, for checks, validated against the schema. */")
	w.line("public Permission<?> relationByName(String name) {")
	w.indent()
	byName(relCases, "relation")
	w.dedent()
	w.line("}")
	w.blank()
	w.line("/** Builds a relationship by relation name, validating the name and the subject. */")
	w.line("public TypedRelationship relationshipByName(String relation, Subject subject) {")
	w.indent()
	if len(rels) > 0 {
		w.line("switch (relation) {")
		w.indent()
		for _, r := range rels {
			w.line("case " + javaString(r.name) + ":")
			w.indent()
			w.line("if (subject instanceof " + r.writerIface + " s) {")
			w.indent()
			w.line("return " + r.writer + "(s);")
			w.dedent()
			w.line("}")
			w.call("throw notAllowed(", []string{javaString(d.Name), "relation", "subject"}, ");")
			w.dedent()
		}
		w.line("default:")
		w.indent()
	}
	w.call("throw new NotInSchemaException(", []string{javaString(d.Name+" has no relation ") + " + relation"}, ");")
	if len(rels) > 0 {
		w.dedent()
		w.dedent()
		w.line("}")
	}
	w.dedent()
	w.line("}")
	w.dedent()
	w.line("}")

	// Statics.
	var statics []string
	factory := func(name, ret, params, body string) error {
		if err := b.reg.claim("static method", name, src("("+name+")")); err != nil {
			return err
		}
		sw := newWriter(2)
		sw.line("public static " + ret + " " + name + "(" + params + ") {")
		sw.indent()
		sw.line(body)
		sw.dedent()
		sw.line("}")
		statics = append(statics, sw.String())
		return nil
	}
	if err := factory(dp, v.typeName, "String id", "return new "+v.typeName+"(id);"); err != nil {
		return "", nil, dd, err
	}
	if err := factory(dp+"Filter", "Filter", "", "return Filter.of("+javaString(d.Name)+");"); err != nil {
		return "", nil, dd, err
	}
	if wk := (variantKey{Def: d.Name, Wildcard: true}); b.variants[wk] != nil {
		if err := factory(dp+"Wildcard", typeNameOf(wk), "", "return new "+typeNameOf(wk)+"();"); err != nil {
			return "", nil, dd, err
		}
	}
	for _, c := range []struct {
		suffix string
		names  []string
	}{
		{"Relations", relNames(rels)},
		{"Permissions", permNames(perms)},
	} {
		class := dp + c.suffix
		if err := b.reg.claim("type", class, src("(name constants)")); err != nil {
			return "", nil, dd, err
		}
		consts := newRegistry()
		_ = consts.claim("member", "names", "the generated names() method")
		sw := newWriter(2)
		sw.line("public static final class " + class + " {")
		sw.indent()
		sw.line("private " + class + "() {}")
		for _, n := range c.names {
			cn := toUpperSnake(n)
			if err := consts.claim("field", cn, src(fmt.Sprintf("%s name %q", strings.ToLower(strings.TrimSuffix(c.suffix, "s")), n))); err != nil {
				return "", nil, dd, err
			}
			sw.blank()
			sw.line("public static final String " + cn + " = " + javaString(n) + ";")
		}
		sw.blank()
		sw.line("public static List<String> names() {")
		sw.indent()
		quoted := make([]string, len(c.names))
		for i, n := range c.names {
			quoted[i] = javaString(n)
		}
		sw.call("return List.of(", quoted, ");")
		sw.dedent()
		sw.line("}")
		sw.dedent()
		sw.line("}")
		statics = append(statics, sw.String())
	}
	for _, p := range perms {
		name := dp + "_" + toPascalCase(p.name)
		if err := b.reg.claim("static field", name, src(fmt.Sprintf("permission %q (PermissionRef constant)", p.name))); err != nil {
			return "", nil, dd, err
		}
		sw := newWriter(2)
		sw.field("public static final PermissionRef<"+p.iface+"> "+name,
			"new PermissionRef<>("+javaString(d.Name)+", "+javaString(p.name)+")")
		statics = append(statics, sw.String())
	}
	return w.String(), statics, dd, nil
}

func relNames(rels []relInfo) []string {
	var out []string
	for _, r := range rels {
		out = append(out, r.name)
	}
	return out
}

func permNames(perms []permInfo) []string {
	var out []string
	for _, p := range perms {
		out = append(out, p.name)
	}
	return out
}

// --- google-java-format-shaped writer ---------------------------------------------------------

const maxLine = 100

type writer struct {
	b   strings.Builder
	ind int
}

func newWriter(indent int) *writer { return &writer{ind: indent} }

func (w *writer) String() string { return strings.TrimRight(w.b.String(), "\n") }

func (w *writer) indent() { w.ind += 2 }
func (w *writer) dedent() { w.ind -= 2 }

func (w *writer) line(s string) {
	if s == "" {
		w.b.WriteString("\n")
		return
	}
	w.b.WriteString(strings.Repeat(" ", w.ind))
	w.b.WriteString(s)
	w.b.WriteString("\n")
}

func (w *writer) blank() { w.b.WriteString("\n") }

func (w *writer) fits(col int, s string) bool { return col+len(s) <= maxLine }

// header writes a type declaration with an implements/permits list the way google-java-format
// does: one line; else the clause on a +4 continuation line; else one type per line at +8.
func (w *writer) header(decl, keyword string, items []string, suffix string) {
	joined := strings.Join(items, ", ")
	if w.fits(w.ind, decl+" "+keyword+" "+joined+suffix) {
		w.line(decl + " " + keyword + " " + joined + suffix)
		return
	}
	w.line(decl)
	if w.fits(w.ind+4, keyword+" "+joined+suffix) {
		w.b.WriteString(strings.Repeat(" ", w.ind+4) + keyword + " " + joined + suffix + "\n")
		return
	}
	for i, item := range items {
		switch {
		case i == 0:
			w.b.WriteString(strings.Repeat(" ", w.ind+4) + keyword + " " + item + ",\n")
		case i == len(items)-1:
			w.b.WriteString(strings.Repeat(" ", w.ind+8) + item + suffix + "\n")
		default:
			w.b.WriteString(strings.Repeat(" ", w.ind+8) + item + ",\n")
		}
	}
}

// call writes `prefix args suffix` (prefix ends with "("), wrapping the arguments the way
// google-java-format does: one line; else all arguments on a +4 continuation; else one per line.
func (w *writer) call(prefix string, args []string, suffix string) {
	joined := strings.Join(args, ", ")
	if w.fits(w.ind, prefix+joined+suffix) {
		w.line(prefix + joined + suffix)
		return
	}
	w.line(prefix)
	pad := strings.Repeat(" ", w.ind+4)
	if w.fits(w.ind+4, joined+suffix) {
		w.b.WriteString(pad + joined + suffix + "\n")
		return
	}
	for i, a := range args {
		if i == len(args)-1 {
			w.b.WriteString(pad + a + suffix + "\n")
		} else {
			w.b.WriteString(pad + a + ",\n")
		}
	}
}

// field writes `decl = init;`, breaking after "=" when it does not fit.
func (w *writer) field(decl, init string) {
	if w.fits(w.ind, decl+" = "+init+";") {
		w.line(decl + " = " + init + ";")
		return
	}
	w.line(decl + " =")
	w.b.WriteString(strings.Repeat(" ", w.ind+4) + init + ";\n")
}

// --- naming helpers ---------------------------------------------------------------------------

func javaString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func dedupSorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	if len(out) == 0 {
		return out
	}
	result := []string{out[0]}
	for i := 1; i < len(out); i++ {
		if out[i] != out[i-1] {
			result = append(result, out[i])
		}
	}
	return result
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
	first := true
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		if first {
			result.WriteString(strings.ToLower(p[:1]))
			first = false
		} else {
			result.WriteString(strings.ToUpper(p[:1]))
		}
		result.WriteString(p[1:])
	}
	return result.String()
}

// toUpperSnake converts a schema name to a Java constant name.
func toUpperSnake(s string) string {
	return strings.ToUpper(s)
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

// Options lists the options this generator accepts, for the CLI's help.
func (g *Generator) Options() []generator.Option {
	return []generator.Option{
		{Name: "package", Value: "<java.package>", Description: "Java package of the generated classes (required)."},
		{Name: "formatter", Value: "<command>", Description: "Command that reads Java source on stdin and writes formatted source on stdout."},
		{Name: "rename", Value: "<definition>=<JavaName>[,...]", Description: "Identifier stem for a definition whose generated names collide; schema names on the wire are unchanged."},
	}
}
