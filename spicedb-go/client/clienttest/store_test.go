package clienttest_test

import (
	"context"
	"iter"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// relationships is the slice of *client.Client a typical consumer depends on.
type relationships interface {
	Write(context.Context, client.WriteRequest, ...client.WriteOption) (client.WriteResult, error)
	ReadRelationships(context.Context, client.ReadRelationshipsRequest, ...client.ReadRelationshipsOption) iter.Seq2[client.ReadRelationship, error]
	DeleteRelationships(context.Context, client.DeleteRequest, ...client.DeleteOption) (client.DeleteResult, error)
	Check(context.Context, client.PermissionCheck, ...client.CheckOption) (client.CheckResult, error)
	CheckBulk(context.Context, []client.PermissionCheck, ...client.CheckOption) ([]client.CheckResult, error)
}

var (
	_ relationships = (*client.Client)(nil)
	_ relationships = (*clienttest.Store)(nil)
)

func write(t *testing.T, s relationships, build func(*rel.Txn) error) error {
	t.Helper()
	var txn rel.Txn
	require.NoError(t, build(&txn))
	_, err := s.Write(context.Background(), client.WriteRequest{Transaction: txn})
	return err
}

func TestStoreWriteSemantics(t *testing.T) {
	s := clienttest.NewStore(rel.MustFromTuple("org:acme#admin@user:alice"))

	err := write(t, s, func(txn *rel.Txn) error { return txn.Create(rel.MustFromTuple("org:acme#admin@user:alice")) })
	require.ErrorIs(t, err, client.ErrAlreadyExists)
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	// MustMatch fails atomically: the touch is not applied.
	err = write(t, s, func(txn *rel.Txn) error {
		if err := txn.MustMatch(rel.MustNewFilter("org").WithRelation("owner")); err != nil {
			return err
		}
		return txn.Touch(rel.MustFromTuple("org:acme#member@user:bob"))
	})
	require.ErrorIs(t, err, client.ErrFailedPrecondition)
	require.Len(t, s.Relationships(), 1)

	err = write(t, s, func(txn *rel.Txn) error {
		if err := txn.MustNotMatch(rel.MustNewFilter("org").WithRelation("member")); err != nil {
			return err
		}
		if err := txn.Delete(rel.MustFromTuple("org:acme#admin@user:alice")); err != nil {
			return err
		}
		return txn.Touch(rel.MustFromTuple("org:acme#member@user:alice"))
	})
	require.NoError(t, err)

	var got []string
	for r, err := range s.ReadRelationships(context.Background(), client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("org").WithResourceID("acme")}) {
		require.NoError(t, err)
		got = append(got, r.String())
	}
	require.Equal(t, []string{"org:acme#member@user:alice"}, got)

	deleted, err := s.DeleteRelationships(context.Background(), client.DeleteRequest{Filter: rel.MustNewFilter("org"), Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted.DeletedCount)
	require.True(t, deleted.Complete)
}

func TestStoreCheckResults(t *testing.T) {
	s := clienttest.NewStore()
	doc := rel.MustNewObject("document", "readme")
	alice := rel.MustNewSubject(rel.MustNewObject("user", "alice"), "")
	bob := rel.MustNewSubject(rel.MustNewObject("user", "bob"), "")
	s.SetCheckResult(doc, "view", alice, client.CheckResult{Permissionship: client.PermissionshipHasPermission})

	results, err := s.CheckBulk(context.Background(), []client.PermissionCheck{
		{Resource: doc, Permission: "view", Subject: bob, Consistency: consistency.Full()},
		{Resource: doc, Permission: "view", Subject: alice, Consistency: consistency.Full()},
	})
	require.NoError(t, err)
	require.False(t, results[0].HasPermission())
	require.True(t, results[1].HasPermission())
}
