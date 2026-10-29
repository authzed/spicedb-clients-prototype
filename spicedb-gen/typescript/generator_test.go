package typescript

import (
	"strings"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateSample(t *testing.T, opts map[string]string) string {
	t.Helper()
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, opts)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "typed_client.gen.ts", files[0].Path)
	return string(files[0].Content)
}

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "typescript", g.Language())
}

func TestCanonicalRequestsAndDirectStreams(t *testing.T) {
	output := generateSample(t, nil)
	assert.Contains(t, output, "return this.client.check(p.checkRequest(s, c), options);")
	assert.Contains(t, output, "consistency: c,")
	assert.NotContains(t, output, "Promise<AsyncIterableIterator<")
	assert.NotContains(t, output, "async lookupResources(")
	assert.Contains(t, output, "options?: LookupResourcesOptions): AsyncIterableIterator<LookupResource>")
	assert.Contains(t, output, "type WriteResult,")
	assert.Contains(t, output, "...requestOptions,")
	assert.Contains(t, output, "const requestOptions = requestFields(options);")
}

func TestGenerateSampleSchema(t *testing.T) {
	output := generateSample(t, nil)

	assert.Contains(t, output, `DO NOT EDIT`)
	assert.Contains(t, output, `} from "@spicedb/client";`)

	// Refs carry an exact _ref discriminator so variants never assign to each other.
	assert.Contains(t, output, "export interface UserRef extends AnySubject {\n    readonly _ref: \"UserRef\";")
	assert.Contains(t, output, "export interface UserIpRangeRef extends AnySubject {\n    readonly _ref: \"UserIpRangeRef\";")
	assert.Contains(t, output, "export interface TeamMemberRef extends AnySubject {\n    readonly _ref: \"TeamMemberRef\";\n    readonly _type: \"team\";\n    readonly _relation: \"member\";")

	// Factories and statics.
	assert.Contains(t, output, `export function User(id: string): UserRef {`)
	assert.Contains(t, output, `export function Document(id: string): DocumentRef {`)
	assert.Contains(t, output, `Document.view = new PermissionRef("document", "view");`)
	assert.Contains(t, output, `Document.delete = new PermissionRef("document", "delete");`)

	// Permissions are observable Permission values.
	assert.Contains(t, output, `readonly view: Permission<"document", "view">;`)
	assert.Contains(t, output, `view: new Permission("document", id, "view"),`)

	// Writers admit exactly the schema's subjects.
	assert.Contains(t, output, `export type DocumentViewerSubject = UserRef | UserIpRangeRef | UserTimeWindowRef | TeamMemberRef;`)
	assert.Contains(t, output, `export type DocumentEditorSubject = UserRef;`)
	assert.Contains(t, output, `viewer(subject: DocumentViewerSubject): TypedRelationship;`)

	// A userset on a relation: the no-argument overload returns the subject ref.
	assert.Contains(t, output, "    member(): TeamMemberRef;\n    member(subject: TeamMemberSubject): TypedRelationship;")

	// Check overloads, per permission and per relation.
	assert.Contains(t, output, `async check(c: Consistency, p: Permission<"document", "view">, s: DocumentViewCheckSubject, options?: CheckOptions): Promise<CheckResult>;`)
	assert.Contains(t, output, `async check(c: Consistency, p: Permission<"document", "viewer">, s: DocumentViewerRelationCheckSubject, options?: CheckOptions): Promise<CheckResult>;`)
	assert.Contains(t, output, `export type DocumentEditCheckSubject = UserRef;`)
	assert.NotContains(t, output, "Promise<boolean>")

	// Lookups.
	assert.Contains(t, output, `lookupResources(c: Consistency, p: PermissionRef<"document", "view">, s: DocumentViewCheckSubject, options?: LookupResourcesOptions): AsyncIterableIterator<LookupResource>;`)

	// Caveat contexts.
	assert.Contains(t, output, `export interface IpRangeContext {`)
	assert.Contains(t, output, `allowedCidr?: string;`)
	assert.Contains(t, output, `withIpRange(ctx: IpRangeContext): UserIpRangeRef;`)

	// Native capability forwarding and typed writes.
	assert.Contains(t, output, `export class TypedClient {`)
	assert.Contains(t, output, `readonly client: SpiceDBClientInterface;`)
	assert.Contains(t, output, `constructor(client: SpiceDBClientInterface) {`)
	assert.Contains(t, output, `async writeTransaction(txn: TypedTransaction, options?: CallOptions): Promise<WriteResult> {`)
	assert.Contains(t, output, `async checkBulkTyped(c: Consistency, checks: readonly TypedCheck[], options?: CheckOptions): Promise<readonly CheckResult[]> {`)
}

func TestAdoptionPositions(t *testing.T) {
	output := generateSample(t, nil)

	// permission usersets get subject refs, with expiring variants.
	assert.Contains(t, output, "export interface OrganizationCanAdminRef extends AnySubject {")
	assert.Contains(t, output, "    canAdminSubject(): OrganizationCanAdminRef;")
	assert.Contains(t, output, "    readonly canAdmin: Permission<\"organization\", \"can_admin\">;")
	assert.Contains(t, output, "withExpiration(at: Date): OrganizationCanAdminExpiringRef;")
	assert.Contains(t, output, "export type InvitationGrantSubject = OrganizationCanAdminRef;")
	assert.Contains(t, output, "export type InvitationTimedGrantSubject = OrganizationCanAdminExpiringRef;")

	// wildcard refs.
	assert.Contains(t, output, "export function UserWildcard(): UserWildcardRef {")
	assert.Contains(t, output, "export type OrganizationCreatorSubject = UserWildcardRef;")
	assert.Contains(t, output, "export type OrganizationGuestSubject = UserRef | UserWildcardRef;")

	// identity-only keys take base refs.
	assert.Contains(t, output, "export type InvitationInviterSubject = UserExpiringRef;")
	assert.Contains(t, output, "export type InvitationInviterKeySubject = UserRef;")
	assert.Contains(t, output, "inviterKey(subject: InvitationInviterKeySubject): RelationshipKey;")

	// relation checks; usersets expand to their members for checks.
	assert.Contains(t, output, "readonly memberRelation: Permission<\"organization\", \"member\">;")
	assert.Contains(t, output, "export type InvitationAcceptCheckSubject = OrganizationCanAdminRef | UserRef;")
	assert.Contains(t, output, "export type OrganizationCreatorRelationCheckSubject = UserRef;")

	// filters, constants and by-name lookups.
	assert.Contains(t, output, "export function OrganizationFilter(): RelationshipFilter {")
	assert.Contains(t, output, "adminFilter(): RelationshipFilter;")
	assert.Contains(t, output, "export const OrganizationCanAdminType = Object.freeze({ _type: \"organization\", _relation: \"can_admin\" } as const);")
	assert.Contains(t, output, "    canAdmin: \"can_admin\",")
	assert.Contains(t, output, `export const OrganizationRelationNames: readonly OrganizationRelationName[] = Object.freeze(["admin", "member", "creator", "guest"]);`)
	assert.Contains(t, output, `    creator: ["UserWildcardRef"],`)
	assert.Contains(t, output, "permissionByName(name: string): Permission<\"organization\", OrganizationPermissionName>;")
}

func TestCaveatContextPreservesRawNames(t *testing.T) {
	output := generateSample(t, nil)
	// The converter maps the camelCase field to the raw schema name SpiceDB's
	// CEL evaluator looks up, and withIpRange routes through it.
	assert.Contains(t, output, `raw["allowed_cidr"] = ctx.allowedCidr`)
	assert.Contains(t, output, "withIpRange: (context: IpRangeContext) => makeUserIpRangeRef(id, ipRangeContextToRaw(context)),")
	// A caveated subject's context becomes item-level check context.
	assert.Contains(t, output, "const merged = subject._caveatContext === undefined ? context : { ...context, ...subject._caveatContext };")
}

func TestClientModuleOption(t *testing.T) {
	output := generateSample(t, map[string]string{"client-module": "@acme/spicedb-client"})
	assert.Contains(t, output, `} from "@acme/spicedb-client";`)
	assert.NotContains(t, output, `"@spicedb/client"`)

	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	_, err = (&Generator{}).Generate(s, map[string]string{"client-module": `bad"module`})
	require.ErrorContains(t, err, "not a valid module specifier")
	_, err = (&Generator{}).Generate(s, map[string]string{"package": "x"})
	require.ErrorContains(t, err, "unknown TypeScript option --typescript.package")
}

func TestOutputIsNormalized(t *testing.T) {
	output := generateSample(t, nil)
	assert.NotContains(t, output, "\n\n\n")
	assert.NotContains(t, output, " \n")
	assert.NotContains(t, output, "\t")
	assert.True(t, strings.HasSuffix(output, "}\n"))
}

func TestCollisionsFailGeneration(t *testing.T) {
	for name, src := range map[string]string{
		"relation key builder vs relation": `use expiration
definition user {}
definition doc {
	relation viewer: user with expiration
	relation viewer_key: user
}`,
		"relation vs built-in member": `definition user {}
definition doc {
	relation filter: user
}`,
		"permission static vs function property": `definition user {}
definition doc {
	relation viewer: user
	permission name = viewer
}`,
		"definition vs global": `definition date {}`,
		"definition vs schema constants": `definition definitions {}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := schema.ParseString(src)
			require.NoError(t, err)
			_, err = (&Generator{}).Generate(s, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "conflicting TypeScript identifier")
		})
	}
}

func TestGenerateEmptySchema(t *testing.T) {
	files, err := (&Generator{}).Generate(&schema.Schema{}, nil)
	require.NoError(t, err)
	output := string(files[0].Content)
	assert.Contains(t, output, `export class TypedClient {`)
	assert.NotContains(t, output, `export function User`)
}

func TestSchemaLevelNameLookups(t *testing.T) {
	output := generateSample(t, nil)
	// Definition-name constants and type.
	assert.Contains(t, output, `export type DefinitionName = "user" | "team" | "document" | "organization" | "invitation";`)
	assert.Contains(t, output, "export const Definitions = Object.freeze({\n    user: \"user\",")
	assert.Contains(t, output, `export const DefinitionNames: readonly DefinitionName[] = Object.freeze(["user", "team", "document", "organization", "invitation"]);`)
	assert.Contains(t, output, `export type AnyResourceRef = UserRef | TeamRef | DocumentRef | OrganizationRef | InvitationRef;`)
	// Schema-level definition lookup and a resource-less permission ref by names.
	assert.Contains(t, output, `export function definitionByName(name: string): DefinitionInfo {`)
	assert.Contains(t, output, `"document": definitionInfo("document", DocumentRelationNames, DocumentPermissionNames, Document),`)
	assert.Contains(t, output, `export function permissionRefByName(definition: string, permission: string): PermissionRef<DefinitionName, string> {`)
	// Lookup of resources by runtime names on the typed client.
	assert.Contains(t, output, `lookupResourcesByName(c: Consistency, definition: string, permission: string, s: AnySubject, options?: LookupResourcesOptions): AsyncIterableIterator<LookupResource> {`)
}

func TestSchemaTextConstant(t *testing.T) {
	src := "definition user {}\n\n// a \"quoted\" `tick` ${x} \\ comment\ndefinition doc {\n\trelation viewer: user\n}\n"
	s, err := schema.ParseString(src)
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, nil)
	require.NoError(t, err)
	output := string(files[0].Content)
	assert.Contains(t, output, `export const SchemaText: string = "definition user {}\n\n// a \"quoted\" `+"`tick`"+` ${x} \\ comment\ndefinition doc {\n\trelation viewer: user\n}\n";`)
	assert.NotContains(t, output, "\t")
}

func TestEmptySchemaHasEmptyNameLookups(t *testing.T) {
	files, err := (&Generator{}).Generate(&schema.Schema{}, nil)
	require.NoError(t, err)
	output := string(files[0].Content)
	assert.Contains(t, output, `export type DefinitionName = never;`)
	assert.Contains(t, output, `export type AnyResourceRef = never;`)
	assert.Contains(t, output, `export const SchemaText: string = "";`)
}

func TestToPascalCase(t *testing.T) {
	for in, want := range map[string]string{"user": "User", "team_member": "TeamMember", "my_long_name": "MyLongName"} {
		assert.Equal(t, want, toPascalCase(in))
	}
}

func TestToCamelCase(t *testing.T) {
	for in, want := range map[string]string{"allowed_cidr": "allowedCidr", "start": "start", "can_admin": "canAdmin"} {
		assert.Equal(t, want, toCamelCase(in))
	}
}

func TestSubjectRefTypeName(t *testing.T) {
	assert.Equal(t, "UserRef", subjectRefTypeName(schema.SubjectType{Definition: "user"}))
	assert.Equal(t, "TeamMemberRef", subjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member"}))
	assert.Equal(t, "UserIpRangeRef", subjectRefTypeName(schema.SubjectType{Definition: "user", CaveatName: "ip_range"}))
	assert.Equal(t, "UserWildcardRef", subjectRefTypeName(schema.SubjectType{Definition: "user", Wildcard: true}))
	assert.Equal(t, "UserExpiringRef", subjectRefTypeName(schema.SubjectType{Definition: "user", Expiration: true}))
	assert.Equal(t, "TeamMemberTimeWindowExpiringRef", subjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member", CaveatName: "time_window", Expiration: true}))
}

func TestBuildSubjectUnion(t *testing.T) {
	assert.Equal(t, "UserRef | TeamMemberRef", buildSubjectUnion([]schema.SubjectType{{Definition: "user"}, {Definition: "team", Relation: "member"}}))
	assert.Equal(t, "never", buildSubjectUnion(nil))
	assert.Equal(t, "UserRef", buildSubjectUnion([]schema.SubjectType{{Definition: "user"}, {Definition: "user"}}))
}

func TestCelTypeToTS(t *testing.T) {
	assert.Equal(t, "string", celTypeToTS("string"))
	assert.Equal(t, "number", celTypeToTS("int"))
	assert.Equal(t, "boolean", celTypeToTS("bool"))
	assert.Equal(t, "JSONValue", celTypeToTS("foo"))
	assert.Equal(t, "JSONObject", celTypeToTS("map"))
	assert.Equal(t, "readonly JSONValue[]", celTypeToTS("list"))
}
