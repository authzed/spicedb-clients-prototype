package rel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func TestFromTriple(t *testing.T) {
	r, err := rel.FromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Equal(t, "doc1", r.ResourceID)
	require.Equal(t, "viewer", r.ResourceRelation)
	require.Equal(t, "user", r.SubjectType)
	require.Equal(t, "alice", r.SubjectID)
	require.Empty(t, r.SubjectRelation)
}

func TestFromTripleValidation(t *testing.T) {
	_, err := rel.FromTriple("", "doc1", "viewer", "user", "alice", "")
	require.ErrorIs(t, err, rel.ErrInvalidResource)

	_, err = rel.FromTriple("document", "doc1", "viewer", "", "", "")
	require.ErrorIs(t, err, rel.ErrInvalidSubject)
}

func TestMustFromTriple(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.Equal(t, "document", r.ResourceType)

	require.Panics(t, func() {
		rel.MustFromTriple("", "", "", "", "", "")
	})
}

func TestFromTuple(t *testing.T) {
	r, err := rel.FromTuple("document:doc1#viewer@user:alice")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Equal(t, "doc1", r.ResourceID)
	require.Equal(t, "viewer", r.ResourceRelation)
	require.Equal(t, "user", r.SubjectType)
	require.Equal(t, "alice", r.SubjectID)
}

func TestFromTupleWithSubjectRelation(t *testing.T) {
	r, err := rel.FromTuple("document:doc1#viewer@group:eng#member")
	require.NoError(t, err)
	require.Equal(t, "member", r.SubjectRelation)
}

func TestFromTupleInvalid(t *testing.T) {
	_, err := rel.FromTuple("invalid")
	require.Error(t, err)
}

func TestFromObjects(t *testing.T) {
	r, err := rel.FromObjects("document", "doc1", "viewer", "user", "alice")
	require.NoError(t, err)
	require.Equal(t, "document", r.ResourceType)
	require.Empty(t, r.SubjectRelation)
}

func TestWithCaveat(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	r2 := r.WithCaveat("is_public", map[string]any{"public": true})
	require.Equal(t, "is_public", r2.CaveatName)
	require.Equal(t, map[string]any{"public": true}, r2.CaveatContext)
	require.Empty(t, r.CaveatName, "original should be unchanged")
}

func TestWithExpiration(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	exp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	r2 := r.WithExpiration(exp)
	require.NotNil(t, r2.Expiration)
	require.Equal(t, exp, *r2.Expiration)
	require.Nil(t, r.Expiration, "original should be unchanged")
}

func TestString(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	require.Equal(t, "document:doc1#viewer@user:alice", r.String())

	r2 := rel.MustFromTriple("document", "doc1", "viewer", "group", "eng", "member")
	require.Equal(t, "document:doc1#viewer@group:eng#member", r2.String())
}

func TestFilter(t *testing.T) {
	r := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	f := r.Filter()
	require.Equal(t, "document", f.ResourceType)
	require.Equal(t, "doc1", f.ResourceID)
	require.Equal(t, "viewer", f.Relation)
}

func TestNewFilter(t *testing.T) {
	f := rel.NewFilter("document").
		WithResourceID("doc1").
		WithRelation("viewer").
		WithSubjectType("user").
		WithSubjectID("alice")
	require.Equal(t, "document", f.ResourceType)
	require.Equal(t, "doc1", f.ResourceID)
	require.Equal(t, "viewer", f.Relation)
	require.Equal(t, "user", f.SubjectType)
	require.Equal(t, "alice", f.SubjectID)
}

func TestTxn(t *testing.T) {
	r1 := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "")
	r2 := rel.MustFromTriple("document", "doc2", "editor", "user", "bob", "")

	var txn rel.Txn
	txn.Create(r1)
	txn.Touch(r2)
	txn.Delete(r1)
	txn.MustNotMatch(rel.NewFilter("document").WithResourceID("doc3"))

	require.Len(t, txn.V1Updates, 3)
	require.Len(t, txn.Preconditions(), 1)
}

func TestRoundTripProto(t *testing.T) {
	original := rel.MustFromTriple("document", "doc1", "viewer", "user", "alice", "").
		WithCaveat("is_public", map[string]any{"public": true})

	proto := original.ToProto()
	roundTripped := rel.FromProto(proto)

	require.Equal(t, original.ResourceType, roundTripped.ResourceType)
	require.Equal(t, original.ResourceID, roundTripped.ResourceID)
	require.Equal(t, original.ResourceRelation, roundTripped.ResourceRelation)
	require.Equal(t, original.SubjectType, roundTripped.SubjectType)
	require.Equal(t, original.SubjectID, roundTripped.SubjectID)
	require.Equal(t, original.CaveatName, roundTripped.CaveatName)
}
