// Package tuplecompat checks spicedb-go's tuple syntax against SpiceDB's own
// parser and formatter. It lives here because spicedb-gen already depends on
// SpiceDB; the spicedb-go library must not.
package tuplecompat

import (
	"testing"
	"time"

	"github.com/authzed/spicedb/pkg/tuple"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

var tuples = []string{
	"document:readme#viewer@user:alice",
	"document:readme#viewer@team:eng#member",
	"document:readme#viewer@user:*",
	"document:readme#viewer@user:alice[only_name]",
	`document:readme#viewer@user:alice[ip_range:{"cidr":"10.0.0.0/8","n":1.5,"list":["a","]"],"nested":{"b":true}}]`,
	"document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
	"document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]",
	`document:readme#viewer@team:eng#member[ip_range:{"cidr":"x"}][expiration:2030-01-02T03:04:05Z]`,
}

func TestSpiceDBParsesOurTuples(t *testing.T) {
	for _, s := range tuples {
		ours, err := rel.FromTuple(s)
		require.NoError(t, err, s)
		theirs, err := tuple.Parse(ours.String())
		require.NoError(t, err, ours.String())
		requireSame(t, ours, theirs)
	}
}

func TestWeParseSpiceDBTuples(t *testing.T) {
	for _, s := range tuples {
		theirs, err := tuple.Parse(s)
		require.NoError(t, err, s)
		formatted, err := tuple.String(theirs)
		require.NoError(t, err)
		ours, err := rel.FromTuple(formatted)
		require.NoError(t, err, formatted)
		requireSame(t, ours, theirs)
	}
}

func requireSame(t *testing.T, ours rel.Relationship, theirs tuple.Relationship) {
	t.Helper()
	require.Equal(t, theirs.Resource.ObjectType, ours.ResourceType())
	require.Equal(t, theirs.Resource.ObjectID, ours.ResourceID())
	require.Equal(t, theirs.Resource.Relation, ours.Relation())
	require.Equal(t, theirs.Subject.ObjectType, ours.SubjectType())
	require.Equal(t, theirs.Subject.ObjectID, ours.SubjectID())
	subjectRel := ours.SubjectRelation()
	if subjectRel == "" {
		subjectRel = tuple.Ellipsis
	}
	require.Equal(t, theirs.Subject.Relation, subjectRel)
	if theirs.OptionalCaveat == nil {
		require.Empty(t, ours.CaveatName())
	} else {
		require.Equal(t, theirs.OptionalCaveat.CaveatName, ours.CaveatName())
		var want map[string]any
		if theirs.OptionalCaveat.Context != nil {
			want = theirs.OptionalCaveat.Context.AsMap()
		}
		got := ours.CaveatContext()
		if len(want) == 0 {
			require.Empty(t, got)
		} else {
			require.Equal(t, want, got)
		}
	}
	if theirs.OptionalExpiration == nil {
		require.Nil(t, ours.Expiration())
	} else {
		require.NotNil(t, ours.Expiration())
		require.True(t, theirs.OptionalExpiration.Equal(*ours.Expiration()), "%s vs %s", theirs.OptionalExpiration, ours.Expiration())
		require.Equal(t, time.UTC, ours.Expiration().UTC().Location())
	}
}
