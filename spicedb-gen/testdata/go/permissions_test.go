package permissions_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

const schema = `
caveat ip_range(allowed_cidr string) {
    allowed_cidr == "0.0.0.0/0"
}

caveat time_window(start string, end string) {
    start != "" && end != ""
}

definition user {}

definition team {
    relation member: user | team#member
}

definition document {
    relation viewer: user | user with ip_range | user with time_window | team#member
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}`

func newTestClient(t *testing.T) *TypedClient {
	t.Helper()
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	require.NoError(t, err)
	return NewTypedClient(c)
}

func TestTouchAndCheck(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)

	// Write the schema
	_, err := tc.Client.WriteSchema(ctx, schema)
	require.NoError(t, err)

	// Write relationships
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("alice")),
		Document("readme").Editor(User("bob")),
		Document("readme").Owner(User("charlie")),
		Document("readme").Viewer(TeamMember("eng")),
	)
	require.NoError(t, err)

	// Write a caveated relationship
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("dave").WithIpRange(IpRangeContext{}.WithAllowedCidr("10.0.0.0/8"))),
	)
	require.NoError(t, err)

	// Check permissions
	cs := consistency.Full()

	allowed, err := Check(ctx, tc, cs, Document("readme").View(), User("alice"))
	require.NoError(t, err)
	assert.True(t, allowed, "alice should be able to view")

	allowed, err = Check(ctx, tc, cs, Document("readme").Edit(), User("alice"))
	require.NoError(t, err)
	assert.False(t, allowed, "alice should not be able to edit")

	allowed, err = Check(ctx, tc, cs, Document("readme").View(), User("bob"))
	require.NoError(t, err)
	assert.True(t, allowed, "bob should be able to view (via editor)")

	allowed, err = Check(ctx, tc, cs, Document("readme").Edit(), User("bob"))
	require.NoError(t, err)
	assert.True(t, allowed, "bob should be able to edit")

	allowed, err = Check(ctx, tc, cs, Document("readme").Delete(), User("charlie"))
	require.NoError(t, err)
	assert.True(t, allowed, "charlie should be able to delete")

	allowed, err = Check(ctx, tc, cs, Document("readme").View(), TeamMember("eng"))
	require.NoError(t, err)
	assert.True(t, allowed, "team#member eng should be able to view")
}

func TestLookupResources(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)

	cs := consistency.Full()

	var ids []string
	for id, err := range LookupResources(ctx, tc, cs, Document_View, User("alice")) {
		require.NoError(t, err)
		ids = append(ids, id)
	}
	assert.Contains(t, ids, "readme")
}

func TestLookupSubjects(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)

	cs := consistency.Full()

	var ids []string
	for id, err := range LookupSubjects(ctx, tc, cs, Document("readme").View(), UserType) {
		require.NoError(t, err)
		ids = append(ids, id)
	}
	assert.Contains(t, ids, "alice")
	assert.Contains(t, ids, "bob")
	assert.Contains(t, ids, "charlie")
}
