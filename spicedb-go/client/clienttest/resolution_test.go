package clienttest_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func subject(t, id string) rel.SubjectRef { return rel.MustNewSubject(rel.MustNewObject(t, id), "") }

func checkOf(resource, permission string, sub rel.SubjectRef) client.PermissionCheck {
	r := rel.MustFromTuple(resource + "#x@user:x").Resource()
	return client.PermissionCheck{Resource: r, Permission: permission, Subject: sub, Consistency: consistency.Full()}
}

func permissionship(t *testing.T, s *clienttest.Store, c client.PermissionCheck, opts ...client.CheckOption) client.Permissionship {
	t.Helper()
	r, err := s.Check(context.Background(), c, opts...)
	require.NoError(t, err)
	return r.Permissionship
}

func TestCheckResolvesFromStoredRelationships(t *testing.T) {
	caveated, err := rel.MustFromTuple("doc:c#view@user:alice").WithCaveat("on_tuesday", nil)
	require.NoError(t, err)
	s := clienttest.NewStore(
		rel.MustFromTuple("doc:a#view@user:alice"),
		rel.MustFromTuple("doc:a#edit@group:eng#member"),
		rel.MustFromTuple("doc:b#view@user:*"),
		caveated,
		rel.MustFromTuple("doc:d#view@user:alice").WithExpiration(time.Now().Add(-time.Hour)),
	)
	alice := subject("user", "alice")

	cases := []struct {
		name  string
		check client.PermissionCheck
		want  client.Permissionship
	}{
		{"exact subject", checkOf("doc:a", "view", alice), client.PermissionshipHasPermission},
		{"other subject", checkOf("doc:a", "view", subject("user", "bob")), client.PermissionshipNoPermission},
		{"relation is not the permission", checkOf("doc:a", "edit", alice), client.PermissionshipNoPermission},
		{"subject relation must match", checkOf("doc:a", "edit", rel.MustNewSubject(rel.MustNewObject("group", "eng"), "member")), client.PermissionshipHasPermission},
		{"direct subject is not the subject set", checkOf("doc:a", "edit", subject("group", "eng")), client.PermissionshipNoPermission},
		{"wildcard grants direct subjects of its type", checkOf("doc:b", "view", subject("user", "carol")), client.PermissionshipHasPermission},
		{"wildcard does not cross types", checkOf("doc:b", "view", subject("team", "carol")), client.PermissionshipNoPermission},
		{"wildcard does not grant subject sets", checkOf("doc:b", "view", rel.MustNewSubject(rel.MustNewObject("user", "carol"), "member")), client.PermissionshipNoPermission},
		{"caveated relationship is conditional", checkOf("doc:c", "view", alice), client.PermissionshipConditionalPermission},
		{"expired relationship does not grant", checkOf("doc:d", "view", alice), client.PermissionshipNoPermission},
		{"other resource", checkOf("doc:z", "view", alice), client.PermissionshipNoPermission},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, permissionship(t, s, tc.check))
		})
	}
}

func TestCheckResolutionOrder(t *testing.T) {
	s := clienttest.NewStore(rel.MustFromTuple("doc:a#view@user:alice"))
	alice := subject("user", "alice")

	// A resolver answers before the stored relationships.
	var seen []client.PermissionCheck
	s.SetCheckResolver(func(c client.PermissionCheck) (client.CheckResult, bool) {
		seen = append(seen, c)
		if c.Resource.ID() == "a" {
			return client.CheckResult{Permissionship: client.PermissionshipNoPermission}, true
		}
		return client.CheckResult{}, false
	})
	require.Equal(t, client.PermissionshipNoPermission, permissionship(t, s, checkOf("doc:a", "view", alice)))

	// A registered result answers before the resolver.
	s.SetCheckResult(rel.MustNewObject("doc", "a"), "view", alice, client.CheckResult{Permissionship: client.PermissionshipConditionalPermission, MissingContext: []string{"ip"}})
	r, err := s.Check(context.Background(), checkOf("doc:a", "view", alice))
	require.NoError(t, err)
	require.Equal(t, client.PermissionshipConditionalPermission, r.Permissionship)
	require.Equal(t, []string{"ip"}, r.MissingContext)
	require.NotEmpty(t, r.CheckedAt.String())
	require.Len(t, seen, 1)

	// A resolver that declines falls through to the relationships.
	s2 := clienttest.NewStore(rel.MustFromTuple("doc:b#view@user:alice"))
	s2.SetCheckResolver(func(client.PermissionCheck) (client.CheckResult, bool) { return client.CheckResult{}, false })
	require.Equal(t, client.PermissionshipHasPermission, permissionship(t, s2, checkOf("doc:b", "view", alice)))

	// The resolver sees the call's context merged with the check's (the check's keys win).
	seen = nil
	c := checkOf("doc:q", "view", alice)
	c.Context = map[string]any{"a": "item"}
	_, err = s.Check(context.Background(), c, client.WithCheckContext(map[string]any{"a": "call", "b": "call"}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": "item", "b": "call"}, seen[0].Context)
}

func TestGrantAllAppliesToEveryCheckForm(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore()
	s.SetCheckResolver(clienttest.GrantAll)
	generated := checkOf("doc:0193f3a2", "view", subject("user", "u-7c1e"))

	r, err := s.Check(ctx, generated)
	require.NoError(t, err)
	require.True(t, r.HasPermission())

	bulk, err := s.CheckBulk(ctx, []client.PermissionCheck{generated, checkOf("doc:x", "edit", subject("user", "y"))})
	require.NoError(t, err)
	require.True(t, bulk[0].HasPermission())
	require.True(t, bulk[1].HasPermission())

	for r, err := range s.CheckIter(ctx, slices.Values([]client.PermissionCheck{generated})) {
		require.NoError(t, err)
		require.True(t, r.HasPermission())
	}

	s.SetCheckResolver(nil)
	require.Equal(t, client.PermissionshipNoPermission, permissionship(t, s, generated))
}

func lookupIDs(t *testing.T, s *clienttest.Store, req client.LookupResourcesRequest, opts ...client.LookupResourcesOption) ([]string, []client.LookupResource) {
	t.Helper()
	var ids []string
	var items []client.LookupResource
	for r, err := range s.LookupResources(context.Background(), req, opts...) {
		require.NoError(t, err)
		ids = append(ids, r.ResourceID)
		items = append(items, r)
	}
	return ids, items
}

func TestLookupResourcesUsesCheckResolution(t *testing.T) {
	caveated, err := rel.MustFromTuple("doc:c#view@user:alice").WithCaveat("on_tuesday", nil)
	require.NoError(t, err)
	s := clienttest.NewStore(
		rel.MustFromTuple("doc:a#view@user:alice"),
		rel.MustFromTuple("doc:b#edit@user:alice"),
		caveated,
		rel.MustFromTuple("doc:d#view@user:*"),
		rel.MustFromTuple("folder:f#parent@doc:e"), // doc:e appears only as a subject
	)
	s.SetCheckResult(rel.MustNewObject("doc", "e"), "view", subject("user", "alice"), client.CheckResult{Permissionship: client.PermissionshipHasPermission})
	req := client.LookupResourcesRequest{ResourceType: "doc", Permission: "view", Subject: subject("user", "alice"), Consistency: consistency.Full()}

	ids, items := lookupIDs(t, s, req)
	require.Equal(t, []string{"a", "c", "d", "e"}, ids)
	require.Equal(t, client.PermissionshipConditionalPermission, items[1].Permissionship)
	require.NotNil(t, items[1].PartialCaveat)
	require.Nil(t, items[0].PartialCaveat)
	require.Equal(t, clienttest.CursorAfter(4), items[3].AfterResultCursor)
	require.NotEmpty(t, items[0].LookedUpAt.String())

	// The filter drops conditional results; cursors still count them.
	ids, items = lookupIDs(t, s, req, client.WithLookupResourcesPermissionshipFilter(client.LookupHasPermissionOnly))
	require.Equal(t, []string{"a", "d", "e"}, ids)
	require.Equal(t, clienttest.CursorAfter(3), items[1].AfterResultCursor)

	// Limit and cursor page through the results.
	ids, items = lookupIDs(t, s, req, client.WithLookupResourcesLimit(2))
	require.Equal(t, []string{"a", "c"}, ids)
	ids, _ = lookupIDs(t, s, req, client.WithLookupResourcesCursor(items[1].AfterResultCursor))
	require.Equal(t, []string{"d", "e"}, ids)

	// A resolver grants over every object of the type, including generated IDs.
	s.SetCheckResolver(clienttest.GrantAll)
	ids, _ = lookupIDs(t, s, req)
	require.Equal(t, []string{"a", "b", "c", "d", "e"}, ids)

	// Invalid requests and cursors fail like the real client.
	for _, err := range s.LookupResources(context.Background(), client.LookupResourcesRequest{Permission: "view", Subject: subject("user", "alice")}) {
		require.ErrorIs(t, err, client.ErrInvalidArgument)
	}
	for _, err := range s.LookupResources(context.Background(), req, client.WithLookupResourcesCursor("nope")) {
		require.ErrorIs(t, err, client.ErrInvalidArgument)
	}
}

func TestLookupSubjectsUsesCheckResolution(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore(
		rel.MustFromTuple("doc:a#view@user:alice"),
		rel.MustFromTuple("doc:a#view@user:bob"),
		rel.MustFromTuple("doc:a#edit@user:carol"),
		rel.MustFromTuple("doc:a#view@group:eng#member"),
		rel.MustFromTuple("doc:b#view@user:*"),
	)
	doc := rel.MustNewObject("doc", "a")
	s.SetCheckResult(doc, "view", subject("user", "carol"), client.CheckResult{Permissionship: client.PermissionshipConditionalPermission, MissingContext: []string{"ip"}})

	var got []client.LookupSubject
	for r, err := range s.LookupSubjects(ctx, client.LookupSubjectsRequest{Resource: doc, Permission: "view", SubjectType: "user", Consistency: consistency.Full()}) {
		require.NoError(t, err)
		got = append(got, r)
	}
	require.Len(t, got, 3)
	require.Equal(t, "alice", got[0].Subject.SubjectID)
	require.Equal(t, "bob", got[1].Subject.SubjectID)
	require.Equal(t, "carol", got[2].Subject.SubjectID)
	require.Equal(t, client.PermissionshipConditionalPermission, got[2].Subject.Permissionship)
	require.Equal(t, []string{"ip"}, got[2].Subject.PartialCaveat.MissingRequiredContext)

	// The subject relation option selects subject sets.
	var sets []string
	for r, err := range s.LookupSubjects(ctx, client.LookupSubjectsRequest{Resource: doc, Permission: "view", SubjectType: "group", Consistency: consistency.Full()},
		client.WithLookupSubjectsRelation("member")) {
		require.NoError(t, err)
		sets = append(sets, r.Subject.SubjectID)
	}
	require.Equal(t, []string{"eng"}, sets)

	// A wildcard grant reports "*" and every known subject of the type; limits count results.
	var wild []string
	for r, err := range s.LookupSubjects(ctx, client.LookupSubjectsRequest{Resource: rel.MustNewObject("doc", "b"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()},
		client.WithLookupSubjectsLimit(4)) {
		require.NoError(t, err)
		wild = append(wild, r.Subject.SubjectID)
	}
	require.Equal(t, []string{"alice", "bob", "carol", "*"}, wild)
}

func TestLookupsAreRecordedAndInjectable(t *testing.T) {
	ctx := context.Background()
	s := clienttest.NewStore(rel.MustFromTuple("doc:a#view@user:alice"), rel.MustFromTuple("doc:b#view@user:alice"))
	req := client.LookupResourcesRequest{ResourceType: "doc", Permission: "view", Subject: subject("user", "alice"), Consistency: consistency.Full()}
	boom := clienttest.ServerError(clienttest.OpLookupResources, client.CodeUnavailable, "boom")
	s.SetFaultHook(func(f clienttest.Fault) error {
		if f.Stage == clienttest.StageItem && f.Item == 1 {
			return boom
		}
		return nil
	})
	var ids []string
	var gotErr error
	for r, err := range s.LookupResources(ctx, req, client.WithLookupResourcesLimit(5)) {
		if err != nil {
			gotErr = err
			break
		}
		ids = append(ids, r.ResourceID)
	}
	require.Equal(t, []string{"a"}, ids)
	require.True(t, errors.Is(gotErr, client.ErrUnavailable))
	calls := s.CallsOf(clienttest.OpLookupResources)
	require.Len(t, calls, 1)
	require.Equal(t, req, calls[0].Request)
	require.EqualValues(t, 5, calls[0].Options.Limit)

	s.FailNext(clienttest.OpLookupSubjects, boom)
	for _, err := range s.LookupSubjects(ctx, client.LookupSubjectsRequest{Resource: rel.MustNewObject("doc", "a"), Permission: "view", SubjectType: "user"}) {
		require.ErrorIs(t, err, client.ErrUnavailable)
	}
	require.Len(t, s.CallsOf(clienttest.OpLookupSubjects), 1)
}

// The Store never produces an unset permissionship, which the real client
// reports as a malformed response: a registered or resolved result left at
// PermissionshipUnspecified (a zero CheckResult) denies, and lookups skip it.
func TestStoreNeverProducesUnsetPermissionship(t *testing.T) {
	ctx := context.Background()
	alice := subject("user", "alice")
	s := clienttest.NewStore(rel.MustFromTuple("doc:a#view@user:alice"), rel.MustFromTuple("doc:b#view@user:alice"))
	s.SetCheckResult(rel.MustNewObject("doc", "a"), "view", alice, client.CheckResult{})
	s.SetCheckResolver(func(client.PermissionCheck) (client.CheckResult, bool) {
		return client.CheckResult{Permissionship: 99}, true
	})
	for _, id := range []string{"a", "b"} {
		require.Equal(t, client.PermissionshipNoPermission, permissionship(t, s, checkOf("doc:"+id, "view", alice)))
	}
	results, err := s.CheckBulk(ctx, []client.PermissionCheck{checkOf("doc:a", "view", alice), checkOf("doc:b", "view", alice)})
	require.NoError(t, err)
	for _, r := range results {
		require.Equal(t, client.PermissionshipNoPermission, r.Permissionship)
	}
	for r, err := range s.CheckIter(ctx, slices.Values([]client.PermissionCheck{checkOf("doc:a", "view", alice)})) {
		require.NoError(t, err)
		require.Equal(t, client.PermissionshipNoPermission, r.Permissionship)
	}
	for r, err := range s.LookupResources(ctx, client.LookupResourcesRequest{ResourceType: "doc", Permission: "view", Subject: alice}) {
		require.NoError(t, err)
		t.Fatalf("unexpected lookup result %v", r)
	}
}
