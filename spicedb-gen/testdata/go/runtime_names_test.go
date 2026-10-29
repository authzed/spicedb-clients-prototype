package permissions_test

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

// String-keyed code (policy expressions, admin tools, tuple text) reaches
// every typed value: objects, subject sets, relationships, checks and lookups
// in both directions, each validated against the schema.

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

func TestObjectByNameAndParseObject(t *testing.T) {
	o, err := ObjectByName("document", "readme")
	require.NoError(t, err)
	require.Equal(t, Document("readme"), o)
	o, err = ParseObject("organization:acme")
	require.NoError(t, err)
	require.Equal(t, Organization("acme"), o)
	require.Equal(t, "organization", o.DefinitionName())

	_, err = ObjectByName("folder", "x")
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = ParseObject("folder:x")
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = ObjectByName("document", "")
	require.ErrorIs(t, err, rel.ErrInvalidResource)
	for _, bad := range []string{"document", "document:", ":x"} {
		_, err = ParseObject(bad)
		require.Error(t, err, bad)
	}
}

func TestSubjectSetByName(t *testing.T) {
	s, err := SubjectSetByName("team", "eng", "member")
	require.NoError(t, err)
	require.Equal(t, Team("eng").Member(), s)

	// A permission used as a subject set elsewhere is a subject set too.
	s, err = SubjectSetByName("organization", "acme", "can_admin")
	require.NoError(t, err)
	require.Equal(t, Organization("acme").CanAdminSubject(), s)

	// The result is admitted wherever the typed ref is.
	r, err := Document("readme").RelationshipByName("viewer", mustSubjectSet(t, "team", "eng", "member"))
	require.NoError(t, err)
	require.Equal(t, Document("readme").Viewer(Team("eng").Member()), r)

	for _, bad := range [][3]string{
		{"folder", "x", "member"},           // unknown definition
		{"team", "x", "owner"},              // unknown relation
		{"document", "x", "viewer"},         // a relation no relation admits as a subject
		{"organization", "x", "can_admin#"}, // not a name
	} {
		_, err := SubjectSetByName(bad[0], bad[1], bad[2])
		require.ErrorIs(t, err, ErrNotInSchema, "%v", bad)
	}
	_, err = SubjectSetByName("team", "", "member")
	require.ErrorIs(t, err, rel.ErrInvalidResource)
}

func mustSubjectSet(t *testing.T, definition, id, relation string) Subject {
	t.Helper()
	s, err := SubjectSetByName(definition, id, relation)
	require.NoError(t, err)
	return s
}

func TestRelationshipFromTriple(t *testing.T) {
	r, err := RelationshipFromTriple("document:readme", "viewer", "user:alice")
	require.NoError(t, err)
	require.Equal(t, Document("readme").Viewer(User("alice")), r)
	r, err = RelationshipFromTriple("document:readme", "viewer", "team:eng#member")
	require.NoError(t, err)
	require.Equal(t, Document("readme").Viewer(Team("eng").Member()), r)
	r, err = RelationshipFromTriple("organization:acme", "creator", "user:*")
	require.NoError(t, err)
	require.Equal(t, Organization("acme").Creator(UserWildcard()), r)
	r, err = RelationshipFromTriple("document:readme", "editor", "user:bob#...")
	require.NoError(t, err)
	require.Equal(t, Document("readme").Editor(User("bob")), r)

	at := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	r, err = RelationshipFromTriple("invitation:i1", "inviter", "user:alice", TripleExpiration(at))
	require.NoError(t, err)
	require.Equal(t, Invitation("i1").Inviter(User("alice").WithExpiration(at)), r)
	r, err = RelationshipFromTriple("document:readme", "viewer", "user:alice", TripleCaveat("ip_range", map[string]any{"allowed_cidr": "10.0.0.0/8"}))
	require.NoError(t, err)
	require.Equal(t, Document("readme").Viewer(User("alice").WithIpRange(IpRangeContext{}.WithAllowedCidr("10.0.0.0/8"))).String(), r.String())

	for _, c := range []struct {
		resource, relation, subject string
		opts                        []TripleOption
		want                        error
	}{
		{"folder:x", "viewer", "user:alice", nil, ErrNotInSchema},
		{"document:x", "viewer", "robot:r", nil, ErrNotInSchema},
		{"document:x", "view", "user:alice", nil, ErrNotInSchema},              // a permission, not a relation
		{"document:x", "editor", "team:eng#member", nil, ErrSubjectNotAllowed}, // editor admits users only
		{"document:x", "viewer", "user:*", nil, ErrSubjectNotAllowed},
		{"organization:x", "creator", "user:alice", nil, ErrSubjectNotAllowed}, // wildcard only
		{"invitation:x", "inviter", "user:alice", nil, ErrSubjectNotAllowed},   // expiration required
		{"document:x", "editor", "user:alice", []TripleOption{TripleExpiration(at)}, ErrSubjectNotAllowed},
		{"document:x", "editor", "user:alice", []TripleOption{TripleCaveat("ip_range", nil)}, ErrSubjectNotAllowed},
		{"document", "viewer", "user:alice", nil, rel.ErrInvalidResource},
		{"document:x", "viewer", "alice", nil, rel.ErrInvalidSubject},
	} {
		_, err := RelationshipFromTriple(c.resource, c.relation, c.subject, c.opts...)
		require.ErrorIs(t, err, c.want, "%s#%s@%s", c.resource, c.relation, c.subject)
	}
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
	store := clienttest.NewStore(
		rel.MustFromTuple("document:readme#view@user:alice"),
		rel.MustFromTuple("document:readme#viewer@user:alice"),
	)
	tc := NewTypedClient(store)
	cs := consistency.Full()

	res, err := CheckByName(ctx, tc, cs, "document", "readme", "view", User("alice"))
	require.NoError(t, err)
	require.True(t, res.HasPermission())
	res, err = CheckByName(ctx, tc, cs, "document", "readme", "viewer", User("alice"))
	require.NoError(t, err)
	require.True(t, res.HasPermission(), "relations check by name too")
	res, err = CheckByName(ctx, tc, cs, "document", "readme", "view", User("bob"))
	require.NoError(t, err)
	require.False(t, res.HasPermission())

	// A by-name permission from a ref also goes through the typed Check.
	d, err := DefinitionByName("document")
	require.NoError(t, err)
	perm, err := d.Ref("readme").PermissionByName("view")
	require.NoError(t, err)
	res, err = Check(ctx, tc, cs, perm, User("alice"))
	require.NoError(t, err)
	require.True(t, res.HasPermission())

	results, err := CheckBulkByName(ctx, tc, cs, []NamedCheck{
		{Definition: "document", ID: "readme", Permission: "view", Subject: User("alice")},
		{Definition: "document", ID: "readme", Permission: "view", Subject: User("bob")},
	})
	require.NoError(t, err)
	require.True(t, results[0].HasPermission())
	require.False(t, results[1].HasPermission())
	calls := len(store.Calls())

	_, err = CheckByName(ctx, tc, cs, "document", "readme", "nope", User("alice"))
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = CheckByName(ctx, tc, cs, "folder", "readme", "view", User("alice"))
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = CheckByName(ctx, tc, cs, "document", "readme", "view", Organization("o1"))
	require.ErrorIs(t, err, ErrSubjectNotAllowed)
	_, err = CheckByName(ctx, tc, cs, "organization", "o1", "view", UserWildcard())
	require.ErrorIs(t, err, ErrSubjectNotAllowed)
	_, err = CheckBulkByName(ctx, tc, cs, []NamedCheck{
		{Definition: "document", ID: "readme", Permission: "view", Subject: User("alice")},
		{Definition: "document", ID: "readme", Permission: "share", Subject: User("alice")},
	})
	var bulkErr *client.BulkCheckError
	require.ErrorAs(t, err, &bulkErr)
	require.Equal(t, 1, bulkErr.Index)
	require.True(t, errors.Is(err, ErrNotInSchema))
	require.Len(t, store.Calls(), calls, "invalid names fail before any call")
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

	// Unknown names, and a wildcard of a reachable type, still fail before any call.
	for _, names := range [][2]string{{"folder", "view"}, {"document", "nope"}} {
		_, err = collect(t, LookupResourcesByName(ctx, tc, cs, names[0], names[1], User("alice")))
		require.ErrorIs(t, err, ErrNotInSchema, "%v", names)
	}
	_, err = collect(t, LookupResourcesByName(ctx, tc, cs, "organization", "view", UserWildcard()))
	require.ErrorIs(t, err, ErrSubjectNotAllowed)
	require.Len(t, store.CallsOf(clienttest.OpLookupResources), 1)

	subjects, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "readme", "viewer", "user", ""))
	require.NoError(t, err)
	require.Len(t, subjects, 1)
	require.Equal(t, "alice", subjects[0].Subject.SubjectID)
	_, err = collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "readme", "viewer", "user", "..."))
	require.NoError(t, err, `"..." is a direct subject`)
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 2)

	for _, st := range [][2]string{{"organization", ""}, {"team", ""}, {"organization", "member"}} {
		subjects, err = collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "readme", "view", st[0], st[1]))
		require.NoError(t, err, st)
		require.Empty(t, subjects, st)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 2, "unreachable subject types make no call")

	for _, args := range [][4]string{
		{"folder", "view", "user", ""},
		{"document", "nope", "user", ""},
		{"document", "view", "folder", ""},
		{"document", "view", "team", "owner"},
	} {
		_, err = collect(t, LookupSubjectsByName(ctx, tc, cs, args[0], "readme", args[1], args[2], args[3]))
		require.ErrorIs(t, err, ErrNotInSchema, "%v", args)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupSubjects), 2)
}

func TestRuntimeNamesAgainstServer(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)
	cs := consistency.Full()
	_, err := tc.WriteSchema(ctx, client.WriteSchemaRequest{Schema: schema})
	require.NoError(t, err)

	eng := mustSubjectSet(t, "team", "rn-eng", "member")
	viewer, err := RelationshipFromTriple("document:rn-doc", "viewer", "team:rn-eng#member")
	require.NoError(t, err)
	alice, err := ParseObject("user:rn-alice")
	require.NoError(t, err)
	member, err := Team("rn-eng").RelationshipByName("member", alice)
	require.NoError(t, err)
	_, err = tc.Touch(ctx, []TypedRelationship{viewer, member})
	require.NoError(t, err)

	res, err := CheckByName(ctx, tc, cs, "document", "rn-doc", "view", alice)
	require.NoError(t, err)
	require.True(t, res.HasPermission(), "a by-name check")
	results, err := CheckBulkByName(ctx, tc, cs, []NamedCheck{
		{Definition: "document", ID: "rn-doc", Permission: "viewer", Subject: eng},
		{Definition: "document", ID: "rn-doc", Permission: "edit", Subject: alice},
	})
	require.NoError(t, err)
	require.True(t, results[0].HasPermission())
	require.False(t, results[1].HasPermission())

	docs, err := collect(t, LookupResourcesByName(ctx, tc, cs, "document", "viewer", eng))
	require.NoError(t, err)
	require.Len(t, docs, 1, "relations are looked up directly")
	require.Equal(t, "rn-doc", docs[0].ResourceID)

	sets, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "viewer", "team", "member"))
	require.NoError(t, err)
	require.NotEmpty(t, sets, "SpiceDB may report a subject once per path")
	for _, s := range sets {
		require.Equal(t, "rn-eng", s.Subject.SubjectID)
	}

	users, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "view", "user", ""))
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "rn-alice", users[0].Subject.SubjectID)

	none, err := collect(t, LookupSubjectsByName(ctx, tc, cs, "document", "rn-doc", "view", "organization", ""))
	require.NoError(t, err)
	require.Empty(t, none, "the server agrees: organization cannot reach document#view")
	noDocs, err := collect(t, LookupResourcesByName(ctx, tc, cs, "document", "view", Organization("rn-org")))
	require.NoError(t, err)
	require.Empty(t, noDocs)
}
