package permissions_test

import (
	"context"
	"iter"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

// Runtime-name code (policy expressions, admin tools) builds subject sets,
// handles any object through one type, checks and looks up in both directions,
// all validated against the schema.

func collect[T any](t *testing.T, seq iter.Seq2[T, error]) ([]T, error) {
	t.Helper()
	var out []T
	for v, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, nil
}

func TestSubjectSetByName(t *testing.T) {
	s, err := SubjectSetByName("team", "eng", "member")
	require.NoError(t, err)
	require.Equal(t, Team("eng").Member(), s)
	require.Equal(t, "team:eng#member", s.(interface{ String() string }).String())

	// A permission used as a subject set elsewhere is a subject set too.
	s, err = SubjectSetByName("organization", "acme", "can_admin")
	require.NoError(t, err)
	require.Equal(t, Organization("acme").CanAdminSubject(), s)

	// The result is admitted wherever the typed ref is.
	r, err := Document("readme").RelationshipByName("viewer", mustSubjectSet(t, "team", "eng", "member"))
	require.NoError(t, err)
	require.Equal(t, Document("readme").Viewer(Team("eng").Member()), r)

	for _, bad := range [][3]string{
		{"folder", "x", "member"},          // unknown definition
		{"team", "x", "owner"},             // unknown relation
		{"document", "x", "viewer"},        // a relation no relation admits as a subject
		{"organization", "x", "can_admin#"}, // not a name
	} {
		_, err := SubjectSetByName(bad[0], bad[1], bad[2])
		require.ErrorIs(t, err, ErrNotInSchema, "%v", bad)
	}
}

func mustSubjectSet(t *testing.T, definition, id, relation string) Subject {
	t.Helper()
	s, err := SubjectSetByName(definition, id, relation)
	require.NoError(t, err)
	return s
}

func TestDefinitionRefCoversEveryObject(t *testing.T) {
	refs := []DefinitionRef{User("u"), Team("t"), Document("d"), Organization("o"), Invitation("i")}
	for i, name := range DefinitionNames() {
		d, err := DefinitionByName(name)
		require.NoError(t, err)
		ref := d.Ref(refs[i].ID())
		require.Equal(t, refs[i], ref)
		require.Equal(t, name, ref.DefinitionName())
		require.Equal(t, name+":"+ref.ID(), ref.String())
	}
	require.Nil(t, Definition{}.Ref("x"))
}

func TestCheckByName(t *testing.T) {
	ctx := context.Background()
	store := clienttest.NewStore(rel.MustFromTuple("document:readme#view@user:alice"))
	tc := NewTypedClient(store)
	cs := consistency.Full()

	d, err := DefinitionByName("document")
	require.NoError(t, err)
	perm, err := d.Ref("readme").PermissionByName("view")
	require.NoError(t, err)
	res, err := Check(ctx, tc, cs, perm, User("alice"))
	require.NoError(t, err)
	require.True(t, res.HasPermission())

	perm, err = d.Permission("readme", "view")
	require.NoError(t, err)
	results, err := CheckBulk(ctx, tc, cs, ChecksFor(User("alice"), perm))
	require.NoError(t, err)
	require.True(t, results[0].HasPermission())
	results, err = CheckBulk(ctx, tc, cs, ChecksFor(User("bob"), perm))
	require.NoError(t, err)
	require.False(t, results[0].HasPermission())

	_, err = d.Ref("readme").PermissionByName("nope")
	require.ErrorIs(t, err, ErrNotInSchema)
}

func TestLookupsByNameValidateAndShortCircuit(t *testing.T) {
	ctx := context.Background()
	store := clienttest.NewStore(
		rel.MustFromTuple("document:readme#viewer@user:alice"),
		rel.MustFromTuple("document:plan#viewer@user:alice"),
	)
	tc := NewTypedClient(store)
	cs := consistency.Full()

	// Relations are accepted as well as permissions.
	got, err := collect(t, LookupResourcesByName(ctx, tc, cs, "document", "viewer", User("alice")))
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, store.CallsOf(clienttest.OpLookupResources), 1)

	// A subject type that cannot reach the position yields nothing, without a call.
	got, err = collect(t, LookupResourcesByName(ctx, tc, cs, "document", "view", Organization("acme")))
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = collect(t, LookupResourcesByName(ctx, tc, cs, "document", "view", Team("eng")))
	require.NoError(t, err, "team (not team#member) cannot reach document#view")
	require.Empty(t, got)
	require.Len(t, store.CallsOf(clienttest.OpLookupResources), 1)

	// Unknown names still fail before any call.
	for _, names := range [][2]string{{"folder", "view"}, {"document", "nope"}} {
		_, err = collect(t, LookupResourcesByName(ctx, tc, cs, names[0], names[1], User("alice")))
		require.ErrorIs(t, err, ErrNotInSchema, "%v", names)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupResources), 1)

	subjects, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "readme", "viewer", "user"))
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	require.Equal(t, "alice", subjects[0].Subject.SubjectID)
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 1)

	for _, st := range []string{"organization", "team", "organization#member"} {
		subjects, err = collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "readme", "view", st))
		require.NoError(t, err, st)
		require.Empty(t, subjects, st)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 1, "unreachable subject types make no call")

	for _, args := range [][3]string{
		{"folder", "view", "user"},
		{"document", "nope", "user"},
		{"document", "view", "folder"},
		{"document", "view", "team#owner"},
		{"document", "view", "team#"},
	} {
		_, err = collect(t, LookupSubjectsByName(ctx, tc, cs, args[0], "readme", args[1], args[2]))
		require.ErrorIs(t, err, ErrNotInSchema, "%v", args)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 1)
}

func TestRuntimeNamesAgainstServer(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)
	cs := consistency.Full()
	_, err := tc.WriteSchema(ctx, client.WriteSchemaRequest{Schema: schema})
	require.NoError(t, err)

	eng := mustSubjectSet(t, "team", "rn-eng", "member")
	viewer, err := Document("rn-doc").RelationshipByName("viewer", eng)
	require.NoError(t, err)
	member, err := Team("rn-eng").RelationshipByName("member", User("rn-alice"))
	require.NoError(t, err)
	_, err = tc.Touch(ctx, []TypedRelationship{viewer, member})
	require.NoError(t, err)

	d, err := DefinitionByName("document")
	require.NoError(t, err)
	perm, err := d.Ref("rn-doc").PermissionByName("view")
	require.NoError(t, err)
	res, err := Check(ctx, tc, cs, perm, User("rn-alice"))
	require.NoError(t, err)
	require.True(t, res.HasPermission(), "a by-name permission is checkable")

	docs, err := collect(t, LookupResourcesByName(ctx, tc, cs, "document", "viewer", eng))
	require.NoError(t, err)
	require.Len(t, docs, 1, "relations are looked up directly")
	require.Equal(t, "rn-doc", docs[0].ResourceID)

	sets, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "viewer", "team#member"))
	require.NoError(t, err)
	require.Len(t, sets, 1)
	require.Equal(t, "rn-eng", sets[0].Subject.SubjectID)

	users, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "view", "user"))
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "rn-alice", users[0].Subject.SubjectID)

	none, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "view", "organization"))
	require.NoError(t, err)
	require.Empty(t, none, "the server agrees: organization cannot reach document#view")
	noDocs, err := collect(t, LookupResourcesByName(ctx, tc, cs, "document", "view", Organization("rn-org")))
	require.NoError(t, err)
	require.Empty(t, noDocs)
}
