package python

import (
	"strings"
	"testing"

	"github.com/authzed/spicedb-clients/spicedb-gen/generator"
	"github.com/authzed/spicedb-clients/spicedb-gen/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSchema parses the shared sample schema used across generator tests.
func testSchema(t *testing.T) *schema.Schema {
	t.Helper()
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)
	return s
}

// fileContent returns the content of the generated file at path, failing the
// test if it's not present in files.
func fileContent(t *testing.T, files []generator.GeneratedFile, path string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return string(f.Content)
		}
	}
	t.Fatalf("no generated file %q among %d files", path, len(files))
	return ""
}

func TestLanguage(t *testing.T) {
	g := &Generator{}
	assert.Equal(t, "python", g.Language())
}

// TestGeneratesThreeFiles verifies the split output: permissions.py holds the
// flavor-agnostic types, sync.py and aio.py each hold a TypedClient bound to
// their flavor's SpiceDBClient.
//
// The brief illustrating this test assumed a package-level `Generate(schema)`
// function. The real API is a method on *Generator with an opts map:
// `(&Generator{}).Generate(s, nil)` — see generator.go:134.
func TestGeneratesThreeFiles(t *testing.T) {
	g := &Generator{}
	files, err := g.Generate(testSchema(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"permissions.py": false, "sync.py": false, "aio.py": false}
	for _, f := range files {
		if _, ok := want[f.Path]; !ok {
			t.Errorf("unexpected generated file %q", f.Path)
			continue
		}
		want[f.Path] = true
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("missing generated file %q", path)
		}
	}
}

func TestSyncClientHasNoAwait(t *testing.T) {
	g := &Generator{}
	files, err := g.Generate(testSchema(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path != "sync.py" {
			continue
		}
		src := string(f.Content)
		for _, bad := range []string{"async def", "await ", "__aenter__", "spicedb.aio"} {
			if strings.Contains(src, bad) {
				t.Errorf("sync.py must not contain %q", bad)
			}
		}
		if !strings.Contains(src, "from spicedb.sync import SpiceDBClient") {
			t.Error("sync.py must import the sync client")
		}
	}
}

// TestAioClientHasAwait is the mirror-image guard for TestSyncClientHasNoAwait:
// it makes sure aio.py actually kept its async surface rather than both files
// accidentally emitting the same (sync or async) body.
func TestAioClientHasAwait(t *testing.T) {
	g := &Generator{}
	files, err := g.Generate(testSchema(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path != "aio.py" {
			continue
		}
		src := string(f.Content)
		for _, want := range []string{"async def", "await ", "__aenter__", "__aexit__", "from spicedb.aio import SpiceDBClient"} {
			if !strings.Contains(src, want) {
				t.Errorf("aio.py must contain %q", want)
			}
		}
		if strings.Contains(src, "from spicedb.sync import") {
			t.Error("aio.py must not import the sync client")
		}
	}
}

// TestPermissionsFileHasNoClientFlavor verifies permissions.py stays
// flavor-agnostic: no SpiceDBClient import (sync or async) and no TypedClient
// class, since that now lives in sync.py/aio.py.
func TestPermissionsFileHasNoClientFlavor(t *testing.T) {
	g := &Generator{}
	files, err := g.Generate(testSchema(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path != "permissions.py" {
			continue
		}
		src := string(f.Content)
		for _, bad := range []string{"SpiceDBClient", "class TypedClient", "spicedb.sync", "spicedb.aio"} {
			if strings.Contains(src, bad) {
				t.Errorf("permissions.py must not contain %q", bad)
			}
		}
	}
}

func TestToPascalCase(t *testing.T) {
	for input, expected := range map[string]string{
		"user":        "User",
		"team_member": "TeamMember",
		"ip_range":    "IpRange",
		"time_window": "TimeWindow",
		"already":     "Already",
	} {
		assert.Equal(t, expected, toPascalCase(input), "input=%q", input)
	}
}

func TestToSnakeCase(t *testing.T) {
	for input, expected := range map[string]string{
		"user":         "user",
		"team_member":  "team_member",
		"TeamMember":   "team_member",
		"CamelCase":    "camel_case",
		"with_already": "with_already",
	} {
		assert.Equal(t, expected, toSnakeCase(input), "input=%q", input)
	}
}

func TestEscapeKeyword(t *testing.T) {
	// Hard keywords get a trailing underscore.
	for _, kw := range []string{"class", "def", "del", "from", "if", "for", "return", "type"} {
		assert.Equal(t, kw+"_", escapeKeyword(kw))
	}
	// Soft keywords (3.10+) also get escaped.
	assert.Equal(t, "match_", escapeKeyword("match"))
	assert.Equal(t, "case_", escapeKeyword("case"))
	// Commonly-shadowed builtins also get escaped.
	assert.Equal(t, "id_", escapeKeyword("id"))
	assert.Equal(t, "list_", escapeKeyword("list"))
	// Non-keywords pass through.
	assert.Equal(t, "viewer", escapeKeyword("viewer"))
	assert.Equal(t, "edit", escapeKeyword("edit"))
}

func TestCelTypeToPyType(t *testing.T) {
	for input, expected := range map[string]string{
		"string":       "str",
		"int":          "int",
		"uint":         "int",
		"double":       "float",
		"bool":         "bool",
		"duration":     "str",
		"timestamp":    "str",
		"unknown_type": "Any",
	} {
		assert.Equal(t, expected, celTypeToPyType(input), "input=%q", input)
	}
}

func TestGenerateSampleSchema(t *testing.T) {
	s, err := schema.ParseFile("../testdata/sample.zed")
	require.NoError(t, err)

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 3)

	permissions := fileContent(t, files, "permissions.py")
	sync := fileContent(t, files, "sync.py")
	aio := fileContent(t, files, "aio.py")

	// Header — every emitted file carries it.
	for name, output := range map[string]string{"permissions.py": permissions, "sync.py": sync, "aio.py": aio} {
		assert.Contains(t, output, "# Code generated by spicedb-gen. DO NOT EDIT.", "missing header in %s", name)
	}

	// --- permissions.py: flavor-agnostic types only ---

	assert.Contains(t, permissions, "from __future__ import annotations")
	assert.Contains(t, permissions, "from dataclasses import dataclass")
	assert.Contains(t, permissions, "from datetime import datetime")
	assert.Contains(t, permissions, "from typing import Any, ClassVar, TypeAlias")
	assert.Contains(t, permissions, "from spicedb import Transaction, Relationship, Filter")
	assert.NotContains(t, permissions, "SpiceDBClient")
	assert.NotContains(t, permissions, "class TypedClient")
	assert.NotContains(t, permissions, "collections.abc import AsyncIterator")
	assert.NotContains(t, permissions, "spicedb.consistency import Consistency")

	// Base value types — all frozen dataclasses
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass Permission:")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass PermissionRef:")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass TypedRelationship:")

	// Caveat context dataclasses
	assert.Contains(t, permissions, "class IpRangeContext:")
	assert.Contains(t, permissions, "allowed_cidr: str | None = None")
	assert.Contains(t, permissions, "class TimeWindowContext:")
	assert.Contains(t, permissions, "end: str | None = None")
	assert.Contains(t, permissions, "start: str | None = None")
	assert.Contains(t, permissions, "def _to_dict(self) -> dict[str, Any]:")

	// The dict-key in _to_dict must be the raw schema param name, not the
	// Python identifier, so SpiceDB's CEL evaluator can bind it correctly.
	assert.Contains(t, permissions, `d["allowed_cidr"] = self.allowed_cidr`)

	// Subject ref dataclasses — base
	assert.Contains(t, permissions, "class User:")
	assert.Contains(t, permissions, "_type: ClassVar[str] = \"user\"")

	// Sub-relation ref (referenced as a subject)
	assert.Contains(t, permissions, "class TeamMember:")
	assert.Contains(t, permissions, "_relation: ClassVar[str] = \"member\"")

	// Caveated variants
	assert.Contains(t, permissions, "class UserIpRange:")
	assert.Contains(t, permissions, "context: IpRangeContext")
	assert.Contains(t, permissions, "_caveat: ClassVar[str] = \"ip_range\"")
	assert.Contains(t, permissions, "class UserTimeWindow:")

	// Internal accessors on every ref
	assert.Contains(t, permissions, "def _subject_ref(self) -> tuple[str, str, str]:")
	assert.Contains(t, permissions, "def _caveat_info(self) -> tuple[str, dict[str, Any]] | None:")
	assert.Contains(t, permissions, "def _expiration(self) -> datetime | None:")

	// Caveat builder methods on User
	assert.Contains(t, permissions, "def with_ip_range(self, ctx: IpRangeContext) -> UserIpRange:")
	assert.Contains(t, permissions, "def with_time_window(self, ctx: TimeWindowContext) -> UserTimeWindow:")

	// UserIpRange's _caveat_info returns the right values
	assert.Contains(t, permissions, "return \"ip_range\", self.context._to_dict()")

	// Document resource ref
	assert.Contains(t, permissions, "class Document:")
	assert.Contains(t, permissions, "    @property")
	assert.Contains(t, permissions, "    def view(self) -> _DocumentViewPermission:")
	assert.Contains(t, permissions, "    def edit(self) -> _DocumentEditPermission:")
	assert.Contains(t, permissions, "    def delete(self) -> _DocumentDeletePermission:")
	assert.Contains(t, permissions, "    def viewer(self, subject: DocumentViewerSubject) -> TypedRelationship:")
	assert.Contains(t, permissions, "    def editor(self, subject: DocumentEditorSubject) -> TypedRelationship:")
	assert.Contains(t, permissions, "    def owner(self, subject: DocumentOwnerSubject) -> TypedRelationship:")

	// Team resource ref — sub-ref property + renamed relation write
	assert.Contains(t, permissions, "class Team:")
	assert.Contains(t, permissions, "    def member(self) -> TeamMember:") // @property
	assert.Contains(t, permissions, "    def for_member(self, subject: TeamMemberSubject) -> TypedRelationship:")

	// Sub-ref property body
	assert.Contains(t, permissions, "return TeamMember(id=self.id)")

	// Per-permission narrowing subclasses — must be frozen dataclasses, since
	// they're constructed at @property access time and shared across calls.
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentViewPermission(Permission): pass")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentEditPermission(Permission): pass")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentDeletePermission(Permission): pass")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentViewRef(PermissionRef): pass")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentEditRef(PermissionRef): pass")
	assert.Contains(t, permissions, "@dataclass(frozen=True)\nclass _DocumentDeleteRef(PermissionRef): pass")

	// Sealed subject TypeAlias unions
	assert.Contains(t, permissions, "DocumentViewerSubject: TypeAlias = TeamMember | User | UserIpRange | UserTimeWindow")
	assert.Contains(t, permissions, "DocumentEditorSubject: TypeAlias = User")
	assert.Contains(t, permissions, "DocumentOwnerSubject: TypeAlias = User")
	assert.Contains(t, permissions, "DocumentViewSubject: TypeAlias = TeamMember | User | UserIpRange | UserTimeWindow")
	assert.Contains(t, permissions, "DocumentEditSubject: TypeAlias = User")
	assert.Contains(t, permissions, "DocumentDeleteSubject: TypeAlias = User")
	assert.Contains(t, permissions, "TeamMemberSubject: TypeAlias = TeamMember | User")

	// Static permission ref constants
	assert.Contains(t, permissions, `DocumentView: _DocumentViewRef = _DocumentViewRef(_resource_type="document", _permission="view")`)
	assert.Contains(t, permissions, `DocumentEdit: _DocumentEditRef = _DocumentEditRef(_resource_type="document", _permission="edit")`)
	assert.Contains(t, permissions, `DocumentDelete: _DocumentDeleteRef = _DocumentDeleteRef(_resource_type="document", _permission="delete")`)

	// TypedTransaction — mixed-op transaction with preconditions
	assert.Contains(t, permissions, "class TypedTransaction:")
	assert.Contains(t, permissions, "_txn: Transaction = field(default_factory=Transaction)")
	assert.Contains(t, permissions, `def create(self, *rels: TypedRelationship) -> "TypedTransaction":`)
	assert.Contains(t, permissions, `def touch(self, *rels: TypedRelationship) -> "TypedTransaction":`)
	assert.Contains(t, permissions, `def delete(self, *rels: TypedRelationship) -> "TypedTransaction":`)
	assert.Contains(t, permissions, `def must_match(self, f: Filter) -> "TypedTransaction":`)
	assert.Contains(t, permissions, `def must_not_match(self, f: Filter) -> "TypedTransaction":`)

	// --- sync.py / aio.py: per-flavor TypedClient ---

	for _, flavor := range []struct {
		name         string
		output       string
		flavorImport string
		defKw        string // "def" or "async def"
		enter, exit  string
		iterKind     string // "Iterator" or "AsyncIterator"
		awaitPrefix  string // "" or "await "
	}{
		{"sync.py", sync, "from spicedb.sync import SpiceDBClient", "def", "__enter__", "__exit__", "Iterator", ""},
		{"aio.py", aio, "from spicedb.aio import SpiceDBClient", "async def", "__aenter__", "__aexit__", "AsyncIterator", "await "},
	} {
		output := flavor.output

		// Imports
		assert.Contains(t, output, "from __future__ import annotations", flavor.name)
		assert.Contains(t, output, "from collections.abc import "+flavor.iterKind, flavor.name)
		assert.Contains(t, output, "from typing import Any, overload", flavor.name)
		assert.Contains(t, output, "from spicedb import Transaction, Relationship, Filter, LookupResource, LookupSubject", flavor.name)
		assert.Contains(t, output, "from spicedb.consistency import Consistency", flavor.name)
		assert.Contains(t, output, flavor.flavorImport, flavor.name)
		assert.Contains(t, output, "from .permissions import (", flavor.name)
		assert.Contains(t, output, "    TypedRelationship,", flavor.name)
		assert.Contains(t, output, "    TypedTransaction,", flavor.name)
		assert.Contains(t, output, "    _DocumentViewPermission,", flavor.name)
		assert.Contains(t, output, "    DocumentViewSubject,", flavor.name)

		// TypedClient skeleton
		assert.Contains(t, output, "class TypedClient:", flavor.name)
		assert.Contains(t, output, "client: SpiceDBClient", flavor.name)
		assert.Contains(t, output, "def __init__(self, client: SpiceDBClient)", flavor.name)
		assert.Contains(t, output, `def connect(cls, endpoint: str, token: str, *, insecure: bool = False) -> "TypedClient":`, flavor.name)
		assert.Contains(t, output, flavor.defKw+" "+flavor.enter, flavor.name)
		assert.Contains(t, output, flavor.defKw+" "+flavor.exit, flavor.name)
		assert.Contains(t, output, flavor.defKw+" close", flavor.name)

		// check — per-permission overloads + generic dispatch
		assert.Contains(t, output, flavor.defKw+" check(self, c: Consistency, p: _DocumentViewPermission, s: DocumentViewSubject) -> bool:", flavor.name)
		assert.Contains(t, output, flavor.defKw+" check(self, c: Consistency, p: _DocumentEditPermission, s: DocumentEditSubject) -> bool:", flavor.name)
		assert.Contains(t, output, flavor.defKw+" check(self, c: Consistency, p: _DocumentDeletePermission, s: DocumentDeleteSubject) -> bool:", flavor.name)

		// writes
		assert.Contains(t, output, flavor.defKw+" touch(self, *rels: TypedRelationship) -> str:", flavor.name)
		assert.Contains(t, output, flavor.defKw+" create(self, *rels: TypedRelationship) -> str:", flavor.name)
		assert.Contains(t, output, flavor.defKw+" delete(self, *rels: TypedRelationship) -> str:", flavor.name)

		// lookup_resources — overload keyed on PermissionRef subclass
		assert.Contains(t, output, "def lookup_resources(self, c: Consistency, p: _DocumentViewRef, s: DocumentViewSubject) -> "+flavor.iterKind+"[LookupResource]:", flavor.name)
		assert.Contains(t, output, "def lookup_resources(self, c: Consistency, p: _DocumentEditRef, s: DocumentEditSubject) -> "+flavor.iterKind+"[LookupResource]:", flavor.name)
		assert.Contains(t, output, "def lookup_resources(self, c: Consistency, p: _DocumentDeleteRef, s: DocumentDeleteSubject) -> "+flavor.iterKind+"[LookupResource]:", flavor.name)

		// lookup_subjects — overload keyed on Permission subclass; sentinel = type[…]
		assert.Contains(t, output, "def lookup_subjects(", flavor.name)
		assert.Contains(t, output, "type[TeamMember] | type[User] | type[UserIpRange] | type[UserTimeWindow]", flavor.name)

		// read_relationships — untyped passthrough
		assert.Contains(t, output, "def read_relationships(self, c: Consistency, f: Filter) -> "+flavor.iterKind+"[Relationship]:", flavor.name)

		// TypedClient.write(txn) — atomic write of a TypedTransaction
		assert.Contains(t, output, flavor.defKw+" write(self, txn: TypedTransaction) -> str:", flavor.name)
		assert.Contains(t, output, "return "+flavor.awaitPrefix+"self.client.write(txn._txn)", flavor.name)
	}

	// sync.py must not carry any async remnants; aio.py must not carry the
	// sync client import. (TestSyncClientHasNoAwait/TestAioClientHasAwait
	// cover this more exhaustively; these are a quick sanity check here.)
	assert.NotContains(t, sync, "async def")
	assert.NotContains(t, aio, "from spicedb.sync import")
}

// TestCaveatParamKeywordCollision verifies that when a caveat parameter's name
// collides with a Python keyword/builtin, the generator:
//   - Escapes the Python identifier (e.g. "id" -> "id_") for the dataclass field
//   - Preserves the raw name as the dict key, so CEL evaluation receives "id",
//     not "id_". A bug in the dict-key mapping would otherwise silently break
//     caveat evaluation at runtime.
func TestPySubjectRefName(t *testing.T) {
	assert.Equal(t, "User", pySubjectRefName(schema.SubjectType{Definition: "user"}))
	assert.Equal(t, "TeamMember", pySubjectRefName(schema.SubjectType{Definition: "team", Relation: "member"}))
	assert.Equal(t, "UserIpRange", pySubjectRefName(schema.SubjectType{Definition: "user", CaveatName: "ip_range"}))
	assert.Equal(t, "UserExpiring", pySubjectRefName(schema.SubjectType{Definition: "user", Expiration: true}))

	// Relation must win when combined with a caveat (sealed-union emitters in
	// later tasks rely on this branch order).
	assert.Equal(t, "TeamMember", pySubjectRefName(schema.SubjectType{
		Definition: "team", Relation: "member", CaveatName: "ip_range",
	}))
}

func TestCaveatParamKeywordCollision(t *testing.T) {
	s := &schema.Schema{
		Caveats: []schema.CaveatDefinition{
			{Name: "needs_id", Params: []schema.CaveatParam{{Name: "id", Type: "string"}}},
		},
		Definitions: []schema.Definition{
			{
				Name: "thing",
				Relations: []schema.Relation{
					{
						Name: "viewer",
						AllowedSubjects: []schema.SubjectType{
							{Definition: "user", CaveatName: "needs_id"},
						},
					},
				},
			},
		},
	}

	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	output := fileContent(t, files, "permissions.py")

	assert.Contains(t, output, "id_: str | None = None",
		"keyword-conflicting param should be escaped on the dataclass field")
	assert.Contains(t, output, `d["id"] = self.id_`,
		"_to_dict must use the raw schema name as the dict key, not the escaped identifier")
}

func TestGenerateEmptySchema(t *testing.T) {
	s := &schema.Schema{}
	g := &Generator{}
	files, err := g.Generate(s, nil)
	require.NoError(t, err)
	require.Len(t, files, 3)

	permissions := fileContent(t, files, "permissions.py")
	sync := fileContent(t, files, "sync.py")
	aio := fileContent(t, files, "aio.py")

	// Base types should still emit in permissions.py; TypedClient in both
	// client files.
	assert.Contains(t, permissions, "class Permission:")
	assert.NotContains(t, permissions, "class TypedClient:")
	assert.Contains(t, sync, "class TypedClient:")
	assert.Contains(t, aio, "class TypedClient:")

	// No definitions, refs, or overloads anywhere.
	for name, output := range map[string]string{"permissions.py": permissions, "sync.py": sync, "aio.py": aio} {
		assert.NotContains(t, output, "class User:", name)
		assert.NotContains(t, output, "class Document:", name)
		assert.NotContains(t, output, "@overload", name)
	}

	// The client files' `from .permissions import (...)` block still holds
	// the two unconditional names even with no schema definitions.
	for name, output := range map[string]string{"sync.py": sync, "aio.py": aio} {
		assert.Contains(t, output, "    TypedRelationship,", name)
		assert.Contains(t, output, "    TypedTransaction,", name)
	}
}

// TestDualRoleDefinitionRejected ensures the generator refuses to emit a
// schema where the same definition is both used as a base subject and carries
// its own permissions/relations/sub-refs. Without this guard, the generated
// file would contain two `class X:` declarations and Python would let the
// second silently win, breaking the subject-ref's caveat builders.
func TestDualRoleDefinitionRejected(t *testing.T) {
	s := &schema.Schema{
		Definitions: []schema.Definition{
			{
				Name: "user",
				Relations: []schema.Relation{
					// user.friend: user — makes "user" appear as a base subject
					// AND gives the user definition a relation of its own.
					{
						Name: "friend",
						AllowedSubjects: []schema.SubjectType{
							{Definition: "user"},
						},
					},
				},
			},
		},
	}

	g := &Generator{}
	_, err := g.Generate(s, nil)
	require.Error(t, err, "dual-role definition must be rejected")
	assert.Contains(t, err.Error(), `"user"`)
	assert.Contains(t, err.Error(), "both")
}
