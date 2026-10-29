package typescript

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
)

func generateFrom(t *testing.T, src string, opts map[string]string) (string, error) {
	t.Helper()
	s, err := schema.ParseString(src)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	if err != nil {
		return "", err
	}
	return string(files[0].Content), nil
}

// collidingSchema is the fixture the TypeScript integration test also
// generates and type-checks (testdata/typescript/rename.zed): `definition
// subject` produces SubjectRef, which the generated module imports from the
// native client, and its caveat calls a function SpiceDB's default CEL
// environment does not declare.
func collidingSchema(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/typescript/rename.zed")
	require.NoError(t, err)
	return string(b)
}

func TestCaveatCallingAnUndeclaredFunctionGenerates(t *testing.T) {
	out, err := generateFrom(t, `
caveat custom_fn(request map<any>, allowed list<string>) {
	customLookup(request).name in allowed
}

definition user {}

definition document {
	relation viewer: user with custom_fn
	permission view = viewer
}`, nil)
	require.NoError(t, err)
	assert.Contains(t, out, "export interface CustomFnContext {")
	assert.Contains(t, out, "withCustomFn(ctx: CustomFnContext): UserCustomFnRef;")
	assert.Contains(t, out, `raw["allowed"] = ctx.allowed`)
}

func TestRenameResolvesDefinitionCollisions(t *testing.T) {
	src := collidingSchema(t)
	_, err := generateFrom(t, src, nil)
	require.ErrorContains(t, err, "SubjectRef")
	require.ErrorContains(t, err, "--typescript.rename", "the collision error must say how to keep the schema")

	out, err := generateFrom(t, src, map[string]string{"rename": "subject=SubjectDef"})
	require.NoError(t, err)
	assert.Contains(t, out, "export function SubjectDef(id: string): SubjectDefRef {")
	assert.Contains(t, out, `_type: "subject",`, "the wire name stays the schema name")
	assert.Contains(t, out, "withCustomFn(ctx: CustomFnContext): SubjectDefCustomFnRef;")
	assert.Contains(t, out, "export const SubjectDefRelations")
	assert.NotContains(t, out, "export interface SubjectRef ")

	// Several renames, comma-separated.
	out, err = generateFrom(t, src, map[string]string{"rename": "subject=SubjectDef, resource=Res"})
	require.NoError(t, err)
	assert.Contains(t, out, "export function Res(id: string): ResRef {")
	assert.Contains(t, out, `Res.view = new PermissionRef("resource", "view");`)

	for _, bad := range []string{"subject", "subject=lower", "subject=Has-Dash", "=Name", "nope=Nope"} {
		_, err := generateFrom(t, src, map[string]string{"rename": bad})
		assert.Error(t, err, bad)
	}
	// A rename can itself collide; that still fails rather than silently renaming.
	_, err = generateFrom(t, src, map[string]string{"rename": "subject=Resource"})
	assert.ErrorContains(t, err, "conflicting TypeScript identifier")
}

func TestKeysOnlyWhereARelationNeedsThem(t *testing.T) {
	output := generateSample(t, nil)
	// inviter admits only expiring users and viewer admits caveated ones, so a
	// key differs from the writer there.
	assert.Contains(t, output, "inviterKey(subject: InvitationInviterKeySubject): RelationshipKey;")
	assert.Contains(t, output, "viewerKey(subject: DocumentViewerKeySubject): RelationshipKey;")
	// editor admits plain users only: the writer's TypedRelationship is the key.
	assert.NotContains(t, output, "editorKey(")
	assert.NotContains(t, output, "DocumentEditorKeySubject")
	assert.NotContains(t, output, "adminKey(")
}

func TestTypedTransactionAndSubjectFilters(t *testing.T) {
	output := generateSample(t, nil)
	assert.Contains(t, output, "get updates(): readonly RelationshipUpdate[] {")
	assert.Contains(t, output, "get preconditions(): readonly Precondition[] {")
	assert.Contains(t, output, "export function withSubjectAnyRelation(filter: RelationshipFilter, subject: AnySubject | SubjectType): RelationshipFilter {")
	assert.Contains(t, output, "It always constrains the subject relation")
}

func TestGeneratedCommentsUseTheRightArticle(t *testing.T) {
	output := generateSample(t, nil)
	assert.Contains(t, output, "/** Refers to an organization resource or subject. */")
	assert.Contains(t, output, "/** Refers to an invitation resource or subject. */")
	assert.Contains(t, output, "/** Refers to a document resource or subject. */")
	assert.Equal(t, "an inviter", withArticle("inviter"))
	assert.Equal(t, "a viewer", withArticle("viewer"))
	assert.Equal(t, "a user", withArticle("user"))
	assert.Equal(t, "a unit", withArticle("unit"))
	assert.Equal(t, "an hour", withArticle("hour"))
	assert.Contains(t, output, "/** Refers to a user resource or subject. */")
}
