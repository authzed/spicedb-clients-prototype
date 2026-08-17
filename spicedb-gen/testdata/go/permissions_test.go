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
		Document("readme").Viewer(Team("eng").Member()),
	)
	require.NoError(t, err)

	// Write a caveated relationship
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("dave").WithIpRange(IpRangeContext{}.WithAllowedCidr("10.0.0.0/8"))),
	)
	require.NoError(t, err)

	// Write a caveated relationship with no context supplied at write time, so
	// checking it needs context the server does not have.
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("erin").WithIpRange(IpRangeContext{})),
	)
	require.NoError(t, err)

	// Check permissions
	cs := consistency.Full()

	result, err := Check(ctx, tc, cs, Document("readme").View(), User("alice"))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "alice should be able to view")

	result, err = Check(ctx, tc, cs, Document("readme").Edit(), User("alice"))
	require.NoError(t, err)
	assert.False(t, result.HasPermission(), "alice should not be able to edit")

	result, err = Check(ctx, tc, cs, Document("readme").View(), User("bob"))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "bob should be able to view (via editor)")

	result, err = Check(ctx, tc, cs, Document("readme").Edit(), User("bob"))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "bob should be able to edit")

	result, err = Check(ctx, tc, cs, Document("readme").Delete(), User("charlie"))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "charlie should be able to delete")

	result, err = Check(ctx, tc, cs, Document("readme").View(), Team("eng").Member())
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "team#member eng should be able to view")

	// A caveated relationship missing its context surfaces as Conditional, not
	// as a bare denial — this is the state a bool return would have collapsed
	// away. It is reachable from the generated Check function because Check
	// returns the full client.CheckResult instead of a bool.
	result, err = Check(ctx, tc, cs, Document("readme").View(), User("erin"))
	require.NoError(t, err)
	assert.False(t, result.HasPermission(), "erin's caveated relationship is missing context, so it is not a grant")
	assert.Equal(t, client.PermissionshipConditionalPermission, result.Permissionship, "erin's check is conditional on the missing ip_range context")
	assert.Contains(t, result.MissingContext, "allowed_cidr")
	assert.NotEmpty(t, result.CheckedAt)

	// The payoff (spec D3b): supplying the missing caveat context at CHECK
	// TIME (not write time), via the new CheckWithContext, resolves erin's
	// CONDITIONAL_PERMISSION into a genuine grant.
	result, err = CheckWithContext(ctx, tc, cs, Document("readme").View(), map[string]any{"allowed_cidr": "0.0.0.0/0"}, User("erin"))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(), "supplying allowed_cidr at check time should resolve erin's conditional check into a grant")
	assert.Equal(t, client.PermissionshipHasPermission, result.Permissionship)
}

func TestLookupResources(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)

	cs := consistency.Full()

	var ids []string
	for res, err := range LookupResources(ctx, tc, cs, Document_View, User("alice")) {
		require.NoError(t, err)
		ids = append(ids, res.ResourceID)
	}
	assert.Contains(t, ids, "readme")
}

func TestLookupSubjects(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)

	cs := consistency.Full()

	var ids []string
	for sub, err := range LookupSubjects(ctx, tc, cs, Document("readme").View(), UserType) {
		require.NoError(t, err)
		ids = append(ids, sub.Subject.SubjectID)
	}
	assert.Contains(t, ids, "alice")
	assert.Contains(t, ids, "bob")
	assert.Contains(t, ids, "charlie")
}
