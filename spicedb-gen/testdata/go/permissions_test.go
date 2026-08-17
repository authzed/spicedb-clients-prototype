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

	// A caveated relationship with no context supplied at write time. The
	// PLAIN Check (no call-level context at all) must still resolve it via
	// the subject's OWN embedded context (User(...).WithIpRange(ctx) passed
	// directly to Check) -- this is what makes the fix functional for the
	// simplest call shape, not just CheckWithContext.
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("frank").WithIpRange(IpRangeContext{})),
	)
	require.NoError(t, err)

	result, err = Check(ctx, tc, cs, Document("readme").View(),
		User("frank").WithIpRange(IpRangeContext{}.WithAllowedCidr("0.0.0.0/0")))
	require.NoError(t, err)
	assert.True(t, result.HasPermission(),
		"frank's plain Check with the subject's own embedded context (no call-level context at all) should resolve to a grant")

	// Merge proof 1 (value-sensitive): the subject's own context must WIN
	// per-key over a conflicting call-level default. The call-level default
	// below is a WRONG cidr that would fail the caveat on its own; the
	// subject's own embedded context supplies the CORRECT cidr.
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("grace").WithIpRange(IpRangeContext{})),
	)
	require.NoError(t, err)

	result, err = CheckWithContext(
		ctx, tc, cs, Document("readme").View(),
		map[string]any{"allowed_cidr": "wrong-value-must-be-overridden"},
		User("grace").WithIpRange(IpRangeContext{}.WithAllowedCidr("0.0.0.0/0")),
	)
	require.NoError(t, err)
	assert.True(t, result.HasPermission(),
		"grace's own allowed_cidr must win over the call-level default that would fail the caveat")

	// Merge proof 2 (presence-sensitive): a call-level key the subject
	// doesn't mention at all must SURVIVE the merge -- this is NOT wholesale
	// replacement. The subject supplies ONLY "start" (via WithTimeWindow); the
	// call-level default supplies "end", a key the subject never mentions. If
	// the merge were wholesale replacement, "end" would be silently dropped
	// and the caveat (start != "" && end != "") would come back Conditional
	// on a MISSING "end", not a grant.
	_, err = tc.Touch(ctx,
		Document("readme").Viewer(User("henry").WithTimeWindow(TimeWindowContext{})),
	)
	require.NoError(t, err)

	result, err = CheckWithContext(
		ctx, tc, cs, Document("readme").View(),
		map[string]any{"end": "5pm"},
		User("henry").WithTimeWindow(TimeWindowContext{}.WithStart("9am")),
	)
	require.NoError(t, err)
	assert.True(t, result.HasPermission(),
		"call-level 'end' (a key the subject doesn't mention) must survive the merge")
	assert.Empty(t, result.MissingContext)
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
