package golang

import (
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "go", g.Language())
}

func TestGenerateSampleSchema(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "permissions.gen.go", files[0].Path)

	output := string(files[0].Content)

	// Generated file header
	assert.Contains(t, output, "DO NOT EDIT")

	// Package and imports
	assert.Contains(t, output, "package permissions")
	assert.Contains(t, output, `"github.com/authzed/spicedb-clients/spicedb-go/client"`)
	assert.Contains(t, output, `"github.com/authzed/spicedb-clients/spicedb-go/consistency"`)
	assert.Contains(t, output, `"github.com/authzed/spicedb-clients/spicedb-go/rel"`)

	// Subject interface
	assert.Contains(t, output, "type Subject interface {")
	assert.Contains(t, output, "subjectRef() (string, string, string)")
	assert.Contains(t, output, "caveatInfo() (string, map[string]any)")
	assert.Contains(t, output, "expirationTime() *time.Time")

	// Permission and PermissionRef types
	assert.Contains(t, output, "type Permission struct {")
	assert.Contains(t, output, "type PermissionRef struct {")

	// TypedClient
	assert.Contains(t, output, "type TypedClient struct {")
	assert.Contains(t, output, "Client *client.Client")
	assert.Contains(t, output, "func NewTypedClient(c *client.Client) *TypedClient {")

	// Caveat context types
	assert.Contains(t, output, "type IpRangeContext struct {")
	assert.Contains(t, output, "AllowedCidr *string")
	assert.Contains(t, output, "func (c IpRangeContext) WithAllowedCidr(v string) IpRangeContext {")

	assert.Contains(t, output, "type TimeWindowContext struct {")
	assert.Contains(t, output, "Start *string")
	assert.Contains(t, output, "End *string")

	// Subject ref types
	assert.Contains(t, output, "type UserRef struct {")
	assert.Contains(t, output, "type TeamMemberRef struct {")

	// Caveated subject types
	assert.Contains(t, output, "type UserIpRange struct {")
	assert.Contains(t, output, "type UserTimeWindow struct {")

	// WithCaveat methods on UserRef
	assert.Contains(t, output, "func (r UserRef) WithIpRange(ctx IpRangeContext) UserIpRange {")
	assert.Contains(t, output, "func (r UserRef) WithTimeWindow(ctx TimeWindowContext) UserTimeWindow {")

	// Caveated type implements subjectRef correctly
	assert.Contains(t, output, `func (r UserIpRange) caveatInfo() (string, map[string]any) {`)

	// Constructors
	assert.Contains(t, output, "func User(id string) UserRef {")
	assert.Contains(t, output, "func Team(id string) TeamRef {")
	assert.Contains(t, output, "func Document(id string) DocumentRef {")

	// SubRef constructors (conflict with relation name "member")
	// SubRef method: Team("eng").Member() returns TeamMemberRef
	assert.Contains(t, output, "func (r TeamRef) Member() TeamMemberRef {")

	// Type sentinels
	assert.Contains(t, output, "var UserType = userRefType{}")
	assert.Contains(t, output, "var TeamMemberType = teamMemberRefType{}")

	// Sealed interfaces
	assert.Contains(t, output, "type DocumentViewerSubject interface {")
	assert.Contains(t, output, "isDocumentViewerSubject()")
	assert.Contains(t, output, "type DocumentEditorSubject interface {")
	assert.Contains(t, output, "isDocumentEditorSubject()")

	// Marker method implementations
	assert.Contains(t, output, "func (UserRef) isDocumentViewerSubject() {}")
	assert.Contains(t, output, "func (UserRef) isDocumentEditorSubject() {}")
	assert.Contains(t, output, "func (UserIpRange) isDocumentViewerSubject() {}")
	assert.Contains(t, output, "func (TeamMemberRef) isDocumentViewerSubject() {}")

	// UserIpRange should NOT implement DocumentEditorSubject
	assert.NotContains(t, output, "func (UserIpRange) isDocumentEditorSubject() {}")
	assert.NotContains(t, output, "func (UserTimeWindow) isDocumentEditorSubject() {}")

	// Permission accessors
	assert.Contains(t, output, "func (d DocumentRef) View() Permission {")
	assert.Contains(t, output, "func (d DocumentRef) Edit() Permission {")
	assert.Contains(t, output, "func (d DocumentRef) Delete() Permission {")

	// Static permission ref vars
	assert.Contains(t, output, "Document_View = PermissionRef{")
	assert.Contains(t, output, "Document_Edit = PermissionRef{")
	assert.Contains(t, output, "Document_Delete = PermissionRef{")

	// Relation methods
	assert.Contains(t, output, "func (d DocumentRef) Viewer(subject DocumentViewerSubject) TypedRelationship {")
	assert.Contains(t, output, "func (d DocumentRef) Editor(subject DocumentEditorSubject) TypedRelationship {")
	assert.Contains(t, output, "func (d DocumentRef) Owner(subject DocumentOwnerSubject) TypedRelationship {")
	// Relation method renamed to ForMember due to SubRef conflict
	assert.Contains(t, output, "func (d TeamRef) ForMember(subject TeamMemberSubject) TypedRelationship {")

	// Check and lookup functions (non-generic, accept Subject interface).
	// Check surfaces the full client.CheckResult — including the Conditional
	// permissionship for a caveated relationship missing context — rather than
	// collapsing it to a bool, which would make that state unreachable.
	assert.Contains(t, output, "func Check(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm Permission, subject Subject) (client.CheckResult, error) {")
	assert.NotContains(t, output, "(bool, error)")
	// The CheckResult returned by the underlying client is passed through
	// untouched, so a Conditional result (and its MissingContext) survives.
	assert.Contains(t, output, "return tc.Client.CheckOne(ctx, cs, perm.permission, r)")
	assert.Contains(t, output, "func LookupResources(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm PermissionRef, subject Subject) iter.Seq2[client.LookupResource, error] {")
	assert.Contains(t, output, "func LookupSubjects(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm Permission, subjectType Subject) iter.Seq2[client.LookupSubject, error] {")

	// Write methods
	assert.Contains(t, output, "func (tc *TypedClient) Touch(ctx context.Context, rels ...TypedRelationship) (string, error) {")
	assert.Contains(t, output, "func (tc *TypedClient) Create(ctx context.Context, rels ...TypedRelationship) (string, error) {")
	assert.Contains(t, output, "func (tc *TypedClient) Delete(ctx context.Context, rels ...TypedRelationship) (string, error) {")
}

// TestCheckAcceptsContext verifies the generator emits a CheckWithContext
// free function alongside Check, mirroring spicedb-go client.Check's own
// CheckWithContext sibling (spec D3b). Check's existing signature must stay
// byte-for-byte unchanged (no existing call site changes); CheckWithContext
// is purely additive and passes checkContext through to CheckOneWithContext
// untouched, not re-derived or dropped.
func TestCheckAcceptsContext(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 1)

	output := string(files[0].Content)

	// Check's existing signature is untouched.
	assert.Contains(t, output, "func Check(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm Permission, subject Subject) (client.CheckResult, error) {")
	assert.Contains(t, output, "return tc.Client.CheckOne(ctx, cs, perm.permission, r)")

	// New CheckWithContext function: checkContext positioned right before the
	// subject parameter, mirroring client.CheckOneWithContext's own parameter
	// order (checkContext immediately before the relationship).
	assert.Contains(t, output, "func CheckWithContext(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm Permission, checkContext map[string]any, subject Subject) (client.CheckResult, error) {")

	// The old context-less CheckWithContext-shaped signature must not exist
	// (guards against a regression that adds the function but drops the
	// checkContext parameter).
	assert.NotContains(t, output, "func CheckWithContext(ctx context.Context, tc *TypedClient, cs consistency.Strategy, perm Permission, subject Subject) (client.CheckResult, error) {")

	// checkContext is passed through to CheckOneWithContext untouched, not
	// dropped or re-derived from the subject.
	assert.Contains(t, output, "return tc.Client.CheckOneWithContext(ctx, cs, perm.permission, checkContext, r)")
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

	// Should still have base types
	assert.Contains(t, output, "type Subject interface {")
	assert.Contains(t, output, "type TypedClient struct {")

	// Should not have any constructors or ref types
	assert.NotContains(t, output, "func User(")
	assert.NotContains(t, output, "type UserRef struct")
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
		{"ip_range", "IpRange"},
		{"time_window", "TimeWindow"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toPascalCase(tt.input))
		})
	}
}

func TestToLowerCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "user"},
		{"team_member", "teamMember"},
		{"ip_range", "ipRange"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toLowerCamel(tt.input))
		})
	}
}

func TestGoSubjectRefTypeName(t *testing.T) {
	assert.Equal(t, "UserRef", goSubjectRefTypeName(schema.SubjectType{Definition: "user"}))
	assert.Equal(t, "TeamMemberRef", goSubjectRefTypeName(schema.SubjectType{Definition: "team", Relation: "member"}))
	assert.Equal(t, "UserIpRange", goSubjectRefTypeName(schema.SubjectType{Definition: "user", CaveatName: "ip_range"}))
	assert.Equal(t, "UserExpiring", goSubjectRefTypeName(schema.SubjectType{Definition: "user", Expiration: true}))
}

func TestCelTypeToGoType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"string", "string"},
		{"int", "int64"},
		{"uint", "uint64"},
		{"double", "float64"},
		{"bool", "bool"},
		{"duration", "string"},
		{"timestamp", "string"},
		{"unknown_type", "any"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, celTypeToGoType(tt.input))
		})
	}
}
