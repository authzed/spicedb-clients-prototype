package java

import (
	"os"
	"strings"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "java", g.Language())
}

func TestGenerateRequiresPackage(t *testing.T) {
	s := &schema.Schema{}
	g := &Generator{}

	_, err := g.Generate(s, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "package")

	_, err = g.Generate(s, map[string]string{})
	require.Error(t, err)

	_, err = g.Generate(s, map[string]string{"package": ""})
	require.Error(t, err)
}

func TestGenerate(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	gen := &Generator{}
	files, err := gen.Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.test"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "Permissions.java", files[0].Path)

	content := string(files[0].Content)
	assert.Contains(t, content, "remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW)")
	assert.Contains(t, content, "SpiceDBClient.createPlaintext(endpoint, token, options)")

	// Write to /tmp for visual inspection.
	if writeErr := os.WriteFile("/tmp/Permissions.java", files[0].Content, 0644); writeErr != nil {
		t.Logf("warning: could not write to /tmp/Permissions.java: %v", writeErr)
	} else {
		t.Logf("wrote generated output to /tmp/Permissions.java")
	}

	// Print the generated output for visual review.
	t.Logf("Generated output:\n%s", content)

	for _, method := range []string{"touch", "create", "delete"} {
		assert.Contains(t, content, "public WriteResult "+method+"(")
		assert.NotContains(t, content, "public ZedToken "+method+"(")
	}

	// Native capabilities are explicit direct forwarders: streams and exceptions are not wrapped.
	for _, signature := range []string{
		"return client.deleteRelationships(filter, limit, options);",
		"return client.deleteAllRelationshipsNonAtomic(filter, options);",
		"return client.updates(objectTypes, startRevision, options);",
		"return client.importRelationships(relationships, options);",
		"return client.exportRelationships(consistency, filter, options);",
		"return client.checkBulk(requests, options);",
		"return client.experimentalWatchPermissionSets(startRevision, options);",
		"return client.experimentalLookupPermissionSets(atRevision, options);",
		"return client.experimentalDownloadPermissionSets(atRevision, options);",
	} {
		assert.Contains(t, content, signature)
	}

	// Check package declaration.
	assert.Contains(t, content, "package com.authzed.spicedb.gen.test;")

	// Check sealed Subject interface.
	assert.Contains(t, content, "public sealed interface Subject\n      permits")

	// Check caveat context classes.
	assert.Contains(t, content, "public static final class IpRangeContext")
	assert.Contains(t, content, "public IpRangeContext withAllowedCidr(String v)")
	assert.Contains(t, content, "public static final class TimeWindowContext")

	// Check constraint sealed interfaces.
	assert.Contains(t, content, "public sealed interface DocumentViewerSubject extends Subject")
	assert.Contains(t, content, "public sealed interface DocumentEditorSubject extends Subject")
	assert.Contains(t, content, "public sealed interface DocumentViewSubject extends Subject")

	// Check base ref record.
	assert.Contains(t, content, "public record UserRef(String id)\n      implements")
	assert.Contains(t, content, `return "user"`)

	// Check caveated ref record.
	assert.Contains(t, content, "public record UserIpRange(String id, IpRangeContext context)\n      implements")
	assert.Contains(t, content, `return "ip_range"`)

	// Check sub-ref record.
	assert.Contains(t, content, "public record TeamMemberRef(String id)\n      implements")

	// Check non-subject definition ref.
	assert.Contains(t, content, "public record DocumentRef(String id)")
	assert.Contains(t, content, "Permission<DocumentViewSubject> view()")

	// Check TeamRef with sub-ref method.
	assert.Contains(t, content, "public record TeamRef(String id)")
	assert.Contains(t, content, "public TeamMemberRef member()")

	// Check generic Permission types.
	assert.Contains(t, content, "public record Permission<S extends Subject>")
	assert.Contains(t, content, "public record PermissionRef<S extends Subject>")

	// Check static PermissionRef constants.
	assert.Contains(t, content, "Document_View")
	assert.Contains(t, content, "Document_Edit")
	assert.Contains(t, content, "Document_Delete")

	// Check type sentinels.
	assert.Contains(t, content, "public static final UserType UserType = new UserType()")
	assert.Contains(t, content, "public static final class UserType\n      implements")
	assert.Contains(t, content, "public static final TeamMemberType TeamMemberType = new TeamMemberType()")

	// Check TypedRelationship record.
	assert.Contains(t, content, "public static final class TypedRelationship implements Deletable {")

	// Check factory methods.
	assert.Contains(t, content, "public static UserRef User(String id)")
	assert.Contains(t, content, "public static DocumentRef Document(String id)")
	assert.Contains(t, content, "public static TeamRef Team(String id)")

	// Check TypedClient class.
	assert.Contains(t, content, "public static final class TypedClient")
	assert.Contains(t, content, "public <S extends Subject> CheckResult check(")
	assert.Contains(t, content, "public <S extends Subject> Stream<LookupResult.LookupResource> lookupResources(")
	assert.Contains(t, content, "public <S extends Subject> Stream<LookupResult.LookupSubject> lookupSubjects(")
	assert.Contains(t, content, "public WriteResult touch(Collection<TypedRelationship> rels)")
	assert.Contains(t, content, "public WriteResult create(Collection<TypedRelationship> rels)")
	assert.Contains(t, content, "public WriteResult delete(Collection<TypedRelationship> rels)")

	// Check relation methods produce TypedRelationship.
	assert.Contains(t, content, "public TypedRelationship viewer(DocumentViewerSubject subject)")
}

// TestCheckSurfacesCheckResult guards against the generated TypedClient.check()
// wrapper re-collapsing spicedb-java's CheckResult (Task 8) back into a bare
// boolean. Root DESIGN.md's "RULE: Only an unconditional grant is true" requires
// the CONDITIONAL_PERMISSION state to remain reachable through every client tier,
// including generated code users don't read.
func TestCheckSurfacesCheckResult(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	gen := &Generator{}
	files, err := gen.Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.test"})
	require.NoError(t, err)
	require.Len(t, files, 1)

	content := string(files[0].Content)

	// Exact new signature: check() must declare CheckResult as its return type.
	assert.Contains(t, content,
		"public <S extends Subject> CheckResult check(Consistency c, Permission<S> perm, S subject) {")

	// Guard against the old boolean form still being present anywhere (e.g. only
	// half of a signature/body pair got updated).
	assert.NotContains(t, content,
		"public <S extends Subject> boolean check(Consistency c, Permission<S> perm, S subject) {")
	assert.NotContains(t, content, "boolean check(")

	// Pass-through return: the wrapper must hand back the underlying
	// checkPermission() result untouched, not unwrap it (e.g. via hasPermission())
	// and rebuild a fresh CheckResult — a regression that would still be
	// type-correct (still returns CheckResult) but would silently collapse
	// CONDITIONAL_PERMISSION into a boolean internally before rewrapping it,
	// discarding permissionship/missingContext/checkedAt.
	assert.Contains(t, content, "return client.check(request, options);")
}

// TestCheckAcceptsContext verifies the generated TypedClient gains a
// context-accepting check() overload matching spicedb-java's own
// checkPermissionWithOptions(Consistency, String, Relationship, CheckOptions) shape (spec D3b),
// and that the pre-existing 3-arg check() is fixed to route the subject's own
// caveat context through the CHECK-TIME r.withCheckContext(...) instead of
// the WRITE-TIME r.withCaveat(...) it was using before -- a genuine
// pre-existing defect: spicedb-java's checkItemFromRel only ever reads
// Relationship.checkContext() for checks, never caveatContext()/caveatName(),
// so withCaveat(...) on the ephemeral relationship built inside check() was
// silently inert (a caveated subject passed to check() supplied no context
// to the actual check RPC at all).
func TestCheckAcceptsContext(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	gen := &Generator{}
	files, err := gen.Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.test"})
	require.NoError(t, err)
	require.Len(t, files, 1)

	content := string(files[0].Content)

	// New overload: context accepted as a trailing Map<String, Object>.
	assert.Contains(t, content,
		"public <S extends Subject> CheckResult check(\n        Consistency c, Permission<S> perm, S subject, CheckOptions options) {")

	// Pass-through: context is handed to the 4-arg checkPermission untouched.
	assert.Contains(t, content, "return client.check(request, options);")

	// The defect fix: check() must route the subject's own caveat context
	// through withCheckContext (CHECK-TIME), never withCaveat (WRITE-TIME) --
	// withCaveat on the ephemeral check relationship has zero effect on the
	// actual check, since checkItemFromRel never reads caveatContext/
	// caveatName. This NotContains guards against the old inert call
	// reappearing in EITHER check() overload.
	assert.Contains(t, content, "var request = perm.forSubject(subject).toPermissionCheck(c);")
	assert.Contains(t, content, "          subjectRef(subject),\n          consistency,\n          subject.caveatContext());")
	assert.Contains(t, content, "return client.check(request, options);")
	assert.NotContains(t, content, "withCheckContext")
	assert.NotContains(t, content, "WithOptions")
	assert.Contains(t, content, "public WriteResult touch(Collection<TypedRelationship> rels, WriteOptions options)")
	assert.Contains(t, content, "return client.write(txn, options);")
	assert.Contains(t, content, "implements AutoCloseable")
	assert.Contains(t, content, "subjectRef(subject), options)")
	assert.Contains(t, content, "options.toBuilder().subjectRelation(relation).build()")
}

func TestBuildTemplateData(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	data, err := buildTemplateData(s, "com.example.permissions")
	require.NoError(t, err)
	assert.Equal(t, "com.example.permissions", data.Package)
	require.Len(t, data.CaveatContexts, 2)

	// Subject shapes: base, caveated, userset (relation and permission), wildcard, expiring.
	for _, name := range []string{
		"UserRef", "UserIpRange", "UserTimeWindow", "UserExpiring", "UserWildcardRef",
		"TeamMemberRef", "OrganizationCanAdminRef", "OrganizationCanAdminExpiring",
		"OrganizationRef", "DocumentRef", "InvitationRef",
	} {
		assert.Contains(t, data.SubjectTypes, name)
	}
	for _, name := range []string{"UserType", "TeamMemberType", "OrganizationCanAdminType", "OrganizationType"} {
		assert.Contains(t, data.SentinelNames, name)
	}
	for _, name := range []string{
		"DocumentViewerSubject", "DocumentViewerKeySubject", "DocumentViewerRelationSubject",
		"DocumentViewSubject", "InvitationGrantSubject", "InvitationTimedGrantSubject",
		"InvitationInviterKeySubject", "OrganizationCreatorSubject", "OrganizationGuestSubject",
	} {
		assert.Contains(t, data.InterfaceNames, name)
	}

	byName := map[string]DefinitionData{}
	for _, d := range data.Defs {
		byName[d.Name] = d
	}
	// A userset on a relation keeps the accessor; the writer moves to forX.
	assert.Equal(t, "forMember", byName["team"].Writers["member"])
	assert.Equal(t, "TeamMemberRef", byName["team"].SubRefMethods["member"])
	// A userset on a permission gets xSubject(), so x() stays the permission accessor.
	assert.Equal(t, "OrganizationCanAdminRef", byName["organization"].SubRefMethods["canAdminSubject"])
	assert.Equal(t, "admin", byName["organization"].Writers["admin"])
}

// TestAdoptionPositions pins the generated surface for the schema positions that
// real migrations could not express: permission usersets, wildcards,
// expiration-only relations and identity keys, relation checks,
// by-name lookups and typed filters/constants.
func TestAdoptionPositions(t *testing.T) {
	content := generateSample(t)
	for _, want := range []string{
		// permission usersets
		"public OrganizationCanAdminRef canAdminSubject() {",
		"public Permission<OrganizationCanAdminSubject> canAdmin() {",
		"public sealed interface InvitationGrantSubject extends Subject permits OrganizationCanAdminRef {}",
		"public OrganizationCanAdminExpiring withExpiration(Instant expiration) {",
		"permits OrganizationCanAdminExpiring {}",
		// wildcards
		"public static UserWildcardRef UserWildcard() {",
		"public sealed interface OrganizationCreatorSubject extends Subject permits UserWildcardRef {}",
		"permits UserRef, UserWildcardRef {}",
		// identity keys
		"public sealed interface InvitationInviterSubject extends Subject permits UserExpiring {}",
		"public sealed interface InvitationInviterKeySubject extends Subject permits UserRef {}",
		"public RelationshipKey inviterKey(InvitationInviterKeySubject subject) {",
		"public record UserExpiring(String id, Instant expiration)",
		// relation checks
		"public Permission<OrganizationMemberRelationSubject> memberRelation() {",
		// by-name lookups
		"public Permission<?> permissionByName(String name) {",
		"public Permission<?> relationByName(String name) {",
		"public TypedRelationship relationshipByName(String relation, Subject subject) {",
		"if (subject instanceof OrganizationCreatorSubject s) {",
		"public static final class OrganizationRelations {",
		`public static final String CAN_ADMIN = "can_admin";`,
		// filters and constants
		"public static Filter OrganizationFilter() {",
		"public Filter adminFilter() {",
		"public static Filter withSubject(Filter filter, Subject subject) {",
		// transactions, bulk checks and native round trips
		"public WriteResult writeTxn(Txn txn, WriteOptions options) {",
		"public List<CheckResult> checkBulk(Consistency c, List<TypedCheck> checks) {",
		"public static TypedRelationship fromRelationship(Relationship relationship) {",
		"public static TypedRelationship ofError(RuntimeException error) {",
	} {
		assert.Contains(t, content, want)
	}
	// Writers never admit type sentinels, and checks of permissions reached through a
	// permission userset admit the users behind it.
	assert.Contains(t, content, "public sealed interface DocumentEditorSubject extends Subject permits UserRef {}")
	assert.Contains(t, content, "permits OrganizationCanAdminRef, OrganizationCanAdminType, UserRef, UserType {}")
	// WriteTxn does not shadow the native write.
	assert.NotContains(t, content, "public WriteResult write(Txn")
}

func generateSample(t *testing.T) string {
	t.Helper()
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"package": "com.authzed.spicedb.gen.test"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	return string(files[0].Content)
}

func TestPackageOption(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	files, err := (&Generator{}).Generate(s, map[string]string{"package": "com.example.authzperms"})
	require.NoError(t, err)
	assert.Contains(t, string(files[0].Content), "\npackage com.example.authzperms;\n")
}

func TestNoLineExceedsFormatterWidth(t *testing.T) {
	// google-java-format's limit; the fixture's spotlessCheck verifies the full format.
	for i, line := range strings.Split(generateSample(t), "\n") {
		assert.LessOrEqual(t, len(line), 100, "line %d: %s", i+1, line)
		assert.False(t, strings.HasSuffix(line, " "), "line %d has trailing whitespace", i+1)
		assert.NotContains(t, line, "\t", "line %d has a tab", i+1)
	}
}

func TestFormatterOption(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	// Any stdin->stdout command is accepted; tr upper-cases to prove the output was piped.
	files, err := (&Generator{}).Generate(s, map[string]string{"package": "p", "formatter": "tr a-z A-Z"})
	require.NoError(t, err)
	assert.Contains(t, string(files[0].Content), "PUBLIC FINAL CLASS PERMISSIONS")

	_, err = (&Generator{}).Generate(s, map[string]string{"package": "p", "formatter": "false"})
	require.Error(t, err)
}

func TestCollisionsFailNamingBothSources(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   []string
	}{
		{
			name: "permission named like a relation accessor",
			schema: `definition user {}
definition doc {
	relation viewer: user
	permission viewer_relation = viewer
}`,
			want: []string{`"DocViewerRelationSubject"`, `relation "viewer" (relation check subjects)`, `permission "viewer_relation"`},
		},
		{
			name: "permission named like a built-in member",
			schema: `definition user {}
definition doc {
	relation viewer: user
	permission filter = viewer
}`,
			want: []string{`"filter"`, `permission "filter"`},
		},
		{
			name: "two relations produce one interface",
			schema: `use expiration
definition user {}
definition doc {
	relation viewer: user with expiration
	relation viewer_key: user
}`,
			want: []string{`"DocViewerKeySubject"`, `relation "viewer" (key subjects)`, `relation "viewer_key" (writer subjects)`},
		},
		{
			name: "reserved word",
			schema: `definition user {}
definition doc {
	relation new: user
}`,
			want: []string{`"new"`, "reserved word"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := schema.ParseString(tc.schema)
			require.NoError(t, err)
			_, err = (&Generator{}).Generate(s, map[string]string{"package": "p"})
			require.Error(t, err)
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user", "User"},
		{"document", "Document"},
		{"team_member", "TeamMember"},
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
		{"allowed_cidr", "allowedCidr"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, toLowerCamel(tt.input))
		})
	}
}

func TestCelTypeToJavaType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"string", "String"},
		{"int", "Long"},
		{"uint", "Long"},
		{"double", "Double"},
		{"bool", "Boolean"},
		{"duration", "String"},
		{"timestamp", "String"},
		{"unknown_type", "Object"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, celTypeToJavaType(tt.input))
		})
	}
}

func TestRuntimeNameSurface(t *testing.T) {
	content := generateSample(t)
	for _, want := range []string{
		// The typed client wraps the client interface, so it also wraps the in-memory double.
		"public TypedClient(SpiceDBApi client)",
		"public TypedClient(SpiceDBApi client, boolean ownsClient)",
		"public SpiceDBApi client()",
		// Native filter-only delete and channel-customizer factories are forwarded.
		"public DeleteResult deleteRelationships(Filter filter) {",
		"SpiceDBClient.createPlaintext(endpoint, token, options, channelCustomizer)",
		"SpiceDBClient.createSystemTls(endpoint, token, options, channelCustomizer)",
		// Schema-level runtime-name lookups.
		"public static Definition definitionByName(String name)",
		`case "document":`,
		"public static PermissionRef<?> permissionRefByName(String definition, String permission)",
		`case "document#view":`,
		"return Document_View;",
		"public sealed interface DefinitionRef extends Subject",
		"Stream<LookupResult.LookupResource> lookupResources(\n        Consistency c, String definition, String permission, Subject subject)",
		// Refs carry their admitted subjects, for runtime validation.
		`new PermissionRef<>("document", "view", DocumentViewSubject.class)`,
		"public record PermissionRef<S extends Subject>(\n      String resourceType, String permission, Class<S> subjectType)",
		// Constants.
		"public static final class Definitions {",
		`public static final String DOCUMENT = "document";`,
		`return List.of("user", "team", "document", "organization", "invitation");`,
		"public static final String SCHEMA =\n      String.join(\n          \"\\n\",\n          \"use expiration\",",
		`"    allowed_cidr == \"0.0.0.0/0\"",`,
	} {
		assert.Contains(t, content, want)
	}
	assert.NotContains(t, content, "TypedClient(SpiceDBClient client")
	assert.NotContains(t, content, "public SpiceDBClient client()")
	// The runtime-name lookup takes a generated Subject; TypedParityTest pins that the native
	// lookupResources signature stays absent.
}

func TestSchemaTextRoundTrips(t *testing.T) {
	src := "// Ünïcode 🚀 \"quoted\" back\\slash\ttab\r\ndefinition user {}\n"
	s, err := schema.ParseString(src)
	require.NoError(t, err)
	assert.Equal(t, src, s.Source)
	files, err := (&Generator{}).Generate(s, map[string]string{"package": "p"})
	require.NoError(t, err)
	content := string(files[0].Content)
	assert.Contains(t, content,
		`"// \u00dcn\u00efcode \ud83d\ude80 \"quoted\" back\\slash\ttab\r",`)
	assert.Contains(t, content, "\"definition user {}\",\n          \"\");")

	empty, err := (&Generator{}).Generate(&schema.Schema{}, map[string]string{"package": "p"})
	require.NoError(t, err)
	assert.Contains(t, string(empty[0].Content), `public static final String SCHEMA = "";`)
}

func TestJavaText(t *testing.T) {
	for in, want := range map[string]string{
		"":           `""`,
		"plain":      `"plain"`,
		`a"b\c`:      `"a\"b\\c"`,
		"\n\r\t\x01": `"\n\r\t\u0001"`,
		"é😀\x7f":     `"\u00e9\ud83d\ude00\u007f"`,
	} {
		assert.Equal(t, want, javaText(in), in)
	}
}

func TestRuntimeNameTypesCollide(t *testing.T) {
	s, err := schema.ParseString("definition user {}\ndefinition doc {\n\trelation viewer: user\n}")
	require.NoError(t, err)
	// A definition whose Java stem is "Definition" would shadow the runtime DefinitionRef type.
	_, err = (&Generator{}).Generate(s, map[string]string{"package": "p", "rename": "doc=Definition"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"DefinitionRef"`)
	assert.Contains(t, err.Error(), "--java.rename")
}
