package rel_test

import (
	"math"
	"testing"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/rel"
	"github.com/stretchr/testify/require"
)

func tuple(t *testing.T, s string) rel.Relationship {
	t.Helper()
	r, e := rel.FromTuple(s)
	require.NoError(t, e)
	return r
}
func TestNativeReferencesAndTupleRoundTrip(t *testing.T) {
	r := tuple(t, "document:doc1#viewer@group:eng#member")
	require.Equal(t, "document", r.Resource().Type())
	require.Equal(t, "doc1", r.Resource().ID())
	require.Equal(t, "viewer", r.Relation())
	require.Equal(t, "group", r.Subject().Object().Type())
	require.Equal(t, "eng", r.Subject().Object().ID())
	require.Equal(t, "member", r.Subject().Relation())
	require.Equal(t, "document:doc1#viewer@group:eng#member", r.String())
	for _, s := range []string{"invalid", "document:#viewer@user:a", "document:d@user:a", "document:d#viewer@user:"} {
		_, e := rel.FromTuple(s)
		require.Error(t, e)
	}
	_, e := rel.NewObject("", "x")
	require.ErrorIs(t, e, rel.ErrInvalidResource)
	_, e = rel.NewSubject(rel.ObjectRef{}, "")
	require.ErrorIs(t, e, rel.ErrInvalidSubject)
	_, e = rel.NewRelationship(r.Resource(), "", r.Subject())
	require.ErrorIs(t, e, rel.ErrInvalidRelation)
}
func TestCaveatSnapshotsNestedJSONAndExpiration(t *testing.T) {
	r := tuple(t, "document:d#viewer@user:a")
	ctx := map[string]any{"nested": map[string]any{"list": []any{nil, true, 42, "value"}}}
	c, e := r.WithCaveat("caveat", ctx)
	require.NoError(t, e)
	ctx["nested"].(map[string]any)["list"].([]any)[1] = false
	copy := c.CaveatContext()
	require.Equal(t, []any{nil, true, float64(42), "value"}, copy["nested"].(map[string]any)["list"])
	copy["nested"].(map[string]any)["list"].([]any)[1] = false
	require.Equal(t, true, c.CaveatContext()["nested"].(map[string]any)["list"].([]any)[1])
	require.Empty(t, r.CaveatName())
	require.Equal(t, "caveat", c.CaveatName())
	exp := time.Now()
	with := c.WithExpiration(exp)
	got := with.Expiration()
	*got = time.Time{}
	require.Equal(t, exp, *with.Expiration())
	require.Nil(t, c.Expiration())
	for _, v := range []any{make(chan int), math.NaN(), math.Inf(1), map[string]any{"bad": make(chan int)}} {
		_, e := r.WithCaveat("c", map[string]any{"offending": v})
		require.ErrorIs(t, e, rel.ErrInvalidCaveatContext)
		require.Contains(t, e.Error(), "offending")
	}
}
func TestExactFilters(t *testing.T) {
	for _, tc := range []struct {
		s string
		k rel.RelationFilterKind
		n string
	}{{"document:d#viewer@user:a", rel.RelationDirect, ""}, {"document:d#viewer@group:g#member", rel.RelationNamed, "member"}} {
		r := tuple(t, tc.s)
		f := r.Filter()
		require.NoError(t, f.Validate())
		require.Equal(t, r.Resource().Type(), f.ResourceType())
		require.Equal(t, r.Resource().ID(), f.ResourceID())
		require.Equal(t, r.Relation(), f.Relation())
		require.Equal(t, r.Subject().Object().Type(), f.SubjectType())
		require.Equal(t, r.Subject().Object().ID(), f.SubjectID())
		require.Equal(t, tc.k, f.SubjectRelation().Kind())
		require.Equal(t, tc.n, f.SubjectRelation().Name())
	}
	f, e := rel.NewFilter("document")
	require.NoError(t, e)
	require.Equal(t, rel.RelationAny, f.SubjectRelation().Kind())
	require.Error(t, f.WithSubjectID("a").Validate())
	require.Error(t, f.WithSubjectRelation(rel.DirectRelation()).Validate())
	require.Error(t, f.WithResourceID("a").WithResourceIDPrefix("a").Validate())
	for _, n := range []string{"", "..."} {
		_, e := rel.NamedRelation(n)
		require.Error(t, e)
	}
	_, e = rel.NewFilter("")
	require.Error(t, e)
}
func TestTxnValidationAndDefensiveCopies(t *testing.T) {
	r := tuple(t, "document:d#viewer@user:a")
	var tx rel.Txn
	require.NoError(t, tx.Create(r))
	require.NoError(t, tx.Touch(r))
	require.NoError(t, tx.Delete(r))
	require.NoError(t, tx.MustMatch(r.Filter()))
	require.NoError(t, tx.MustNotMatch(r.Filter()))
	require.Error(t, tx.Create(rel.Relationship{}))
	require.Error(t, tx.MustMatch(rel.Filter{}))
	u := tx.Updates()
	require.Len(t, u, 3)
	u[0].Operation = rel.UpdateOperationDelete
	require.Equal(t, rel.UpdateOperationCreate, tx.Updates()[0].Operation)
	ps := tx.Preconditions()
	ps[0].Operation = rel.PreconditionMustNotMatch
	require.Equal(t, rel.PreconditionMustMatch, tx.Preconditions()[0].Operation)
	require.Len(t, tx.Preconditions(), 2)
}
