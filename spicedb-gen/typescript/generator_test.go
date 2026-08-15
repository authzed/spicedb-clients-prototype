package typescript

import (
	"strings"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "typescript", g.Language())
}

func TestGenerateSampleSchema(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "typed_client.gen.ts", files[0].Path)

	output := string(files[0].Content)

	// Ref types (include _caveat?: never to prevent structural subtyping with caveated variants)
	assert.Contains(t, output, `type UserRef = { _type: "user"; _id: string; _caveat?: never };`)
	assert.Contains(t, output, `type TeamRef = { _type: "team"; _id: string; _caveat?: never };`)
	assert.Contains(t, output, `type DocumentRef = { _type: "document"; _id: string; _caveat?: never };`)
	assert.Contains(t, output, `type TeamMemberRef = { _type: "team"; _id: string; _relation: "member"; _caveat?: never };`)

	// Factory functions
	assert.Contains(t, output, `export function User(id: string) {`)
	assert.Contains(t, output, `export function Team(id: string) {`)
	assert.Contains(t, output, `export function Document(id: string) {`)

	// Permission properties on Document
	assert.Contains(t, output, `view: { _type: "document" as const, _id: id, _permission: "view" as const },`)
	assert.Contains(t, output, `edit: { _type: "document" as const, _id: id, _permission: "edit" as const },`)
	assert.Contains(t, output, `delete: { _type: "document" as const, _id: id, _permission: "delete" as const },`)

	// Subject constraints on relations (viewer includes caveated variants)
	assert.Contains(t, output, `viewer: (subject: UserRef | UserIpRangeRef | UserTimeWindowRef | TeamMemberRef) => ({`)
	assert.Contains(t, output, `editor: (subject: UserRef) => ({`)
	assert.Contains(t, output, `owner: (subject: UserRef) => ({`)

	// Team member relation (also a SubRef — accepts optional subject with overloaded cast)
	assert.Contains(t, output, `member: ((subject?: UserRef | TeamMemberRef):`)

	// Static properties for lookups
	assert.Contains(t, output, `Document.view = { _type: "document" as const, _permission: "view" as const };`)
	assert.Contains(t, output, `Document.edit = { _type: "document" as const, _permission: "edit" as const };`)
	assert.Contains(t, output, `Document.delete = { _type: "document" as const, _permission: "delete" as const };`)

	// SubRef via relation method (no-arg call returns TeamMemberRef, with-arg returns relation binding)
	assert.Contains(t, output, `member: ((subject?:`)
	assert.Contains(t, output, `{ (): TeamMemberRef; (subject: UserRef | TeamMemberRef)`)
	assert.NotContains(t, output, `Team.member = (id: string)`)

	// TypedClient class
	assert.Contains(t, output, `export class TypedClient {`)
	assert.Contains(t, output, `readonly client: SpiceDBClient;`)
	assert.Contains(t, output, `static create(endpoint: string, token: string`)

	// Check overloads (view includes caveated variants)
	assert.Contains(t, output, `async check(c: Consistency, p: { _type: "document"; _id: string; _permission: "view" }, s: UserRef | UserIpRangeRef | UserTimeWindowRef | TeamMemberRef): Promise<boolean>;`)
	assert.Contains(t, output, `async check(c: Consistency, p: { _type: "document"; _id: string; _permission: "edit" }, s: UserRef): Promise<boolean>;`)
	assert.Contains(t, output, `async check(c: Consistency, p: { _type: "document"; _id: string; _permission: "delete" }, s: UserRef): Promise<boolean>;`)

	// Check implementation
	assert.Contains(t, output, `return this.client.checkPermission(c, {`)

	// Write operations
	assert.Contains(t, output, `async touch(`)
	assert.Contains(t, output, `async create(`)
	assert.Contains(t, output, `async delete(`)

	// LookupResources overloads
	assert.Contains(t, output, `async lookupResources(c: Consistency, p: { _type: "document"; _permission: "view" }, s: UserRef | UserIpRangeRef | UserTimeWindowRef | TeamMemberRef): Promise<AsyncIterableIterator<LookupResource>>;`)

	// LookupSubjects overloads
	assert.Contains(t, output, `async lookupSubjects(c: Consistency, p: { _type: "document"; _id: string; _permission: "view" }`)

	// ReadRelationships
	assert.Contains(t, output, `readRelationships(`)

	// Imports
	assert.Contains(t, output, `import {`)
	assert.Contains(t, output, `SpiceDBClient,`)
	assert.Contains(t, output, `createSpiceDBClient,`)
	assert.Contains(t, output, `Transaction,`)

	// Caveat context interfaces
	assert.Contains(t, output, `export interface IpRangeContext {`)
	assert.Contains(t, output, `allowedCidr?: string;`)
	assert.Contains(t, output, `export interface TimeWindowContext {`)
	assert.Contains(t, output, `start?: string;`)
	assert.Contains(t, output, `end?: string;`)

	// Caveated subject variant types
	assert.Contains(t, output, `type UserIpRangeRef = { _type: "user"; _id: string; _caveat: "ip_range"; _caveatContext: IpRangeContext };`)
	assert.Contains(t, output, `type UserTimeWindowRef = { _type: "user"; _id: string; _caveat: "time_window"; _caveatContext: TimeWindowContext };`)

	// Caveat methods on User factory
	assert.Contains(t, output, `withIpRange: (ctx: IpRangeContext): UserIpRangeRef`)
	assert.Contains(t, output, `withTimeWindow: (ctx: TimeWindowContext): UserTimeWindowRef`)

	// Write operations include caveat fields (caveatContext cast as any for JsonObject compatibility)
	assert.Contains(t, output, `caveatName: r._subject._caveat`)
	assert.Contains(t, output, `caveatContext: r._subject._caveatContext as any`)

	// Generated file header
	assert.Contains(t, output, `DO NOT EDIT`)
}

func TestGenerateEmptySchema(t *testing.T) {
	s := &schema.Schema{
		Definitions: []schema.Definition{},
	}

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 1)

	output := string(files[0].Content)

	// Should still have a valid TypedClient
	assert.Contains(t, output, `export class TypedClient {`)
	assert.Contains(t, output, `readonly client: SpiceDBClient;`)

	// Should not have any factories
	assert.NotContains(t, output, `export function`)
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "User"},
		{"document", "Document"},
		{"team_member", "TeamMember"},
		{"my_long_name", "MyLongName"},
		{"already", "Already"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toPascalCase(tt.input))
		})
	}
}

func TestBuildSubjectUnion(t *testing.T) {
	subjects := []schema.SubjectType{
		{Definition: "user"},
		{Definition: "team", Relation: "member"},
	}
	assert.Equal(t, "UserRef | TeamMemberRef", buildSubjectUnion(subjects))

	// Empty should return "never"
	assert.Equal(t, "never", buildSubjectUnion(nil))

	// Dedup
	subjects = []schema.SubjectType{
		{Definition: "user"},
		{Definition: "user"},
	}
	result := buildSubjectUnion(subjects)
	assert.Equal(t, "UserRef", result)
	assert.Equal(t, 1, strings.Count(result, "UserRef"))
}

func TestSubjectRefTypeName(t *testing.T) {
	assert.Equal(t, "UserRef", subjectRefTypeName(schema.SubjectType{Definition: "user"}))
	assert.Equal(t, "TeamMemberRef", subjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member"}))
	assert.Equal(t, "UserIpRangeRef", subjectRefTypeName(schema.SubjectType{Definition: "user", CaveatName: "ip_range"}))
	assert.Equal(t, "TeamMemberTimeWindowRef", subjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member", CaveatName: "time_window"}))
}

func TestToCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"allowed_cidr", "allowedCidr"},
		{"start", "start"},
		{"my_long_name", "myLongName"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toCamelCase(tt.input))
		})
	}
}

func TestCelTypeToTS(t *testing.T) {
	assert.Equal(t, "string", celTypeToTS("string"))
	assert.Equal(t, "number", celTypeToTS("int"))
	assert.Equal(t, "number", celTypeToTS("uint"))
	assert.Equal(t, "number", celTypeToTS("double"))
	assert.Equal(t, "boolean", celTypeToTS("bool"))
	assert.Equal(t, "string", celTypeToTS("duration"))
	assert.Equal(t, "string", celTypeToTS("timestamp"))
	assert.Equal(t, "unknown", celTypeToTS("foo"))
}

func TestBuildSubjectUnionWithCaveats(t *testing.T) {
	subjects := []schema.SubjectType{
		{Definition: "user"},
		{Definition: "user", CaveatName: "ip_range"},
		{Definition: "team", Relation: "member"},
	}
	assert.Equal(t, "UserRef | UserIpRangeRef | TeamMemberRef", buildSubjectUnion(subjects))
}
