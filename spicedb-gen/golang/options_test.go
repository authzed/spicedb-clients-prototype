package golang

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func generate(t *testing.T, zed string, opts map[string]string) (string, error) {
	t.Helper()
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	if err != nil {
		return "", err
	}
	return string(files[0].Content), nil
}

func TestPackageOptionAndFormatting(t *testing.T) {
	out, err := generate(t, "definition user {}\ndefinition doc {\n\trelation viewer: user\n}", map[string]string{"package": "authzperms"})
	require.NoError(t, err)
	assert.Contains(t, out, "package authzperms\n")
	formatted, err := format.Source([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, string(formatted), out, "output must already be gofmt-clean")

	for _, bad := range []string{"", "1abc", "func", "my-pkg"} {
		_, err := generate(t, "definition user {}", map[string]string{"package": bad})
		assert.Error(t, err, bad)
	}
	_, err = generate(t, "definition user {}", map[string]string{"unknown": "x"})
	assert.Error(t, err)
}

// The schema from the adoption report: a relation whose subject is a userset
// on a permission used to reference an interface that was never emitted.
func TestPermissionUsersetSubject(t *testing.T) {
	out, err := generate(t, `
definition user {}
definition org {
	relation admin: user
	permission can_admin = admin
}
definition inv {
	relation grant: org#can_admin
}`, nil)
	require.NoError(t, err)
	out = squash(out)
	assert.Contains(t, out, "type OrgCanAdminRef struct")
	assert.Contains(t, out, "func (d OrgRef) CanAdminSubject() OrgCanAdminRef")
	assert.Contains(t, out, "func (d OrgRef) CanAdmin() Permission")
	assert.Contains(t, out, "type InvGrantSubject interface")
	assert.Contains(t, out, "func (OrgCanAdminRef) isInvGrantSubject() {}")
	assert.Contains(t, out, "var OrgCanAdminType = ")
}

func TestConflictingIdentifiersFail(t *testing.T) {
	// relation "viewer_relation" collides with the check accessor of "viewer".
	_, err := generate(t, `
definition user {}
definition doc {
	relation viewer: user
	relation viewer_relation: user
	permission view = viewer
}`, nil)
	require.ErrorContains(t, err, "ViewerRelation")
}

const collidingSchema = `
caveat custom_fn(request map<any>) {
	customLookup(request).allowed
}

definition subject {}

definition resource {
	relation viewer: subject | subject with custom_fn
	permission view = viewer
}`

func TestRenameResolvesDefinitionCollisions(t *testing.T) {
	_, err := generate(t, collidingSchema, nil)
	require.ErrorContains(t, err, "Subject")
	require.ErrorContains(t, err, "--go.rename", "the collision error must say how to keep the schema")

	out, err := generate(t, collidingSchema, map[string]string{"rename": "subject=SubjectDef"})
	require.NoError(t, err)
	flat := squash(out)
	assert.Contains(t, flat, "func SubjectDef(id string) SubjectDefRef")
	assert.Contains(t, flat, `return "subject", r.id, ""`, "the wire name stays the schema name")
	assert.Contains(t, flat, "func (r SubjectDefRef) WithCustomFn(ctx CustomFnContext) SubjectDefCustomFn")

	for _, bad := range []string{"subject", "subject=lower", "nope=Nope"} {
		_, err := generate(t, collidingSchema, map[string]string{"rename": bad})
		assert.Error(t, err, bad)
	}

	if testing.Short() {
		return
	}
	// The renamed output compiles against the real native client.
	dir, err := os.MkdirTemp("../testdata/go", "zz_rename_")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "permissions.gen.go"), []byte(out), 0o644))
	cmd := exec.Command("go", "vet", "./"+filepath.Base(dir))
	cmd.Dir = "../testdata/go"
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	vetOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(vetOut))
}

func TestWithArticle(t *testing.T) {
	for word, want := range map[string]string{
		"user": "a user", "inviter": "an inviter", "organization": "an organization",
		"document": "a document", "UserRef": "a UserRef", "OrganizationCanAdminRef": "an OrganizationCanAdminRef",
		"unit": "a unit", "hour": "an hour", "admin": "an admin",
	} {
		assert.Equal(t, want, withArticle(word), word)
	}
}

func TestSchemaTextAndDefinitionNames(t *testing.T) {
	zed := "definition user {}\n\n// a `quoted` comment\ndefinition doc {\n\trelation viewer: user\n\tpermission view = viewer\n}\n"
	out, err := generate(t, zed, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "const SchemaText = "+strconv.Quote(zed), "a backquote forces an interpreted literal holding the text verbatim")
	flat := squash(out)
	assert.Contains(t, flat, `var Definitions = struct { User string Doc string }{ User: "user", Doc: "doc", }`)
	assert.Contains(t, flat, `return []string{"user", "doc"}`)
	assert.Contains(t, flat, `"doc": {name: "doc", relations: DocRelationNames(), permissions: DocPermissionNames(), ref: func(id string) DefinitionRef { return Doc(id) }},`)
	assert.Contains(t, flat, "func PermissionRefByName(definition, permission string) (PermissionRef, error)")
	assert.Contains(t, flat, "func LookupResourcesByName(ctx context.Context, tc *TypedClient, cs consistency.Strategy, definition, permission string, subject Subject, opts ...client.LookupResourcesOption)")

	out, err = generate(t, "definition user {}", nil)
	require.NoError(t, err)
	assert.Contains(t, out, "const SchemaText = `definition user {}`")
	assert.Equal(t, "`a\nb`", goStringLiteral("a\nb"))
	assert.Equal(t, `"a\r\nb"`, goStringLiteral("a\r\nb"))
}

func TestByNameIdentifiersAreReserved(t *testing.T) {
	_, err := generate(t, "definition definitions {}", nil)
	require.ErrorContains(t, err, "Definitions")
	require.ErrorContains(t, err, "--go.rename")
	_, err = generate(t, "definition definitions {}", map[string]string{"rename": "definitions=Defs"})
	require.NoError(t, err)
	_, err = generate(t, "definition definition_ref {}", nil)
	require.ErrorContains(t, err, "DefinitionRef")
	_, err = generate(t, "definition user {}\ndefinition doc {\n\trelation viewer: user\n\tpermission definition_name = viewer\n}", nil)
	require.ErrorContains(t, err, "DefinitionName", "every ref has DefinitionName, so a permission cannot take it")
}

func TestRuntimeNameTables(t *testing.T) {
	zed := `definition user {}
definition group {
	relation member: user | group#member
}
definition doc {
	relation viewer: user | group#member
	relation parent: group
	permission view = viewer
}`
	out, err := generate(t, zed, nil)
	require.NoError(t, err)
	flat := squash(out)
	assert.Contains(t, flat, `"doc#view": {"group#member", "user"},`, "subject sets expand to their members")
	assert.Contains(t, flat, `"doc#parent": {"group"},`)
	assert.Contains(t, flat, `case "group#member": if _, err := rel.NewObject(definition, id); err != nil { return nil, err } return GroupMemberRef{id: id}, nil`)
	assert.NotContains(t, flat, `case "doc#viewer":`, "a relation never used as a subject set has no subject set")
}

func TestNamesOnly(t *testing.T) {
	zed := "caveat c(x int) { x > 0 }\ndefinition user {}\ndefinition doc {\n\trelation viewer: user | user with c\n\tpermission view = viewer\n}\n"
	s, err := schema.ParseString(zed)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"names-only": "true", "package": "apinames"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "names.gen.go", files[0].Path)
	out := string(files[0].Content)

	f, err := parser.ParseFile(token.NewFileSet(), "names.gen.go", out, parser.ImportsOnly)
	require.NoError(t, err)
	assert.Empty(t, f.Imports, "names-only output imports nothing, so it never depends on the client")
	assert.Equal(t, "apinames", f.Name.Name)

	flat := squash(out)
	assert.Contains(t, flat, `var Definitions = struct { User string Doc string }{ User: "user", Doc: "doc", }`)
	assert.Contains(t, flat, `var DocRelations = struct { Viewer string }{ Viewer: "viewer", }`)
	assert.Contains(t, flat, `var DocPermissions = struct { View string }{ View: "view", }`)
	assert.Contains(t, flat, "func DocRelationNames() []string")
	assert.Contains(t, flat, "func DefinitionNames() []string")
	assert.Contains(t, out, "const SchemaText = `"+zed+"`")
	for _, absent := range []string{"TypedClient", "DocRef", "CContext", "Permission{", "DefinitionByName"} {
		assert.NotContains(t, out, absent)
	}

	// Renames still apply, so identifiers match the full client's.
	out, err = generate(t, collidingSchema, map[string]string{"names-only": "true", "rename": "subject=SubjectDef"})
	require.NoError(t, err)
	assert.Contains(t, squash(out), `SubjectDef: "subject"`)

	// false (or no option) generates the full client.
	out, err = generate(t, zed, map[string]string{"names-only": "false"})
	require.NoError(t, err)
	assert.Contains(t, out, "type TypedClient struct")

	_, err = generate(t, zed, map[string]string{"names-only": "yes please"})
	require.ErrorContains(t, err, "--go.names-only")

	// The default package differs from the full output's, so a file can import
	// both without an alias.
	out, err = generate(t, zed, map[string]string{"names-only": "true"})
	require.NoError(t, err)
	assert.Contains(t, out, "package permissionnames\n")
	out, err = generate(t, zed, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "package permissions\n")
}

// testdata/go-names is a module with no requirements holding names-only
// output; it must match what the generator emits today.
func TestNamesOnlyFixtureIsCurrent(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"names-only": "true", "package": "apinames"})
	require.NoError(t, err)
	want, err := os.ReadFile("../testdata/go-names/names.gen.go")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(files[0].Content),
		"regenerate: spicedb-gen --schema testdata/sample.zed --lang go --go.package=apinames --go.names-only --out testdata/go-names/names.gen.go")

	if testing.Short() {
		return
	}
	// The fixture module requires nothing, so this builds only while the
	// output imports the standard library alone.
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = "../testdata/go-names"
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	vetOut, err := cmd.CombinedOutput()
	require.NoError(t, err, string(vetOut))
}
