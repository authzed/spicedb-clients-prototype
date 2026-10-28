package permissions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

// These tests cover the typed-client capabilities found missing while
// migrating real services: permission usersets, wildcard refs, identity-only
// keys, relation checks, typed transactions, filters, bulk checks, native
// round trips and by-name lookups. The first group needs no server.

func TestPermissionsAreObservable(t *testing.T) {
	p := Organization("acme").CanAdmin()
	require.Equal(t, "organization", p.ResourceType())
	require.Equal(t, "acme", p.ResourceID())
	require.Equal(t, "can_admin", p.Name())
	require.Equal(t, "organization:acme#can_admin", p.String())
	require.Equal(t, "organization#can_admin", p.Ref().String())
	require.Equal(t, "document#view", Document_View.String())
	require.Equal(t, "organization:acme#member", Organization("acme").MemberRelation().String())
}

func TestTypedRelationshipsRoundTrip(t *testing.T) {
	typed := Invitation("inv").Grant(Organization("acme").CanAdminSubject())
	require.Equal(t, "invitation:inv#grant@organization:acme#can_admin", typed.String())
	native, err := typed.Relationship()
	require.NoError(t, err)
	require.Equal(t, typed.String(), FromRelationship(native).String())

	broken := ErrRelationship(errors.New("no such user"))
	require.Contains(t, broken.String(), "no such user")
	require.Error(t, FromRelationship(rel.Relationship{}).Err(), "zero native relationship is invalid")
}

func TestTxnDefersFirstError(t *testing.T) {
	txn := (&Txn{}).
		Touch(Organization("").Admin(User("alice"))). // empty ID: deferred error
		Touch(Organization("acme").Admin(User("bob")))
	_, err := txn.Build()
	require.ErrorIs(t, err, rel.ErrInvalidResource)

	ok := (&Txn{}).
		MustNotMatch(Organization("acme").AdminFilter()).
		Delete(Organization("acme").Member(User("alice"))).
		Touch(Organization("acme").Admin(User("alice")))
	require.Equal(t, 2, ok.Len())
	native, err := ok.Build()
	require.NoError(t, err)
	require.Equal(t, "[MUST_NOT_MATCH(organization:acme#admin@*), DELETE(organization:acme#member@user:alice), TOUCH(organization:acme#admin@user:alice)]", native.String())
}

func TestTypedFilters(t *testing.T) {
	require.Equal(t, "organization:*#*@*", OrganizationFilter().String())
	require.Equal(t, "organization:acme#*@*", Organization("acme").Filter().String())
	require.Equal(t, "organization:acme#admin@*", Organization("acme").AdminFilter().String())
	require.Equal(t, "organization:acme#admin@user:alice#...", WithSubject(Organization("acme").AdminFilter(), User("alice")).String())
	require.Equal(t, "invitation:*#*@organization:*#can_admin", WithSubject(InvitationFilter(), OrganizationCanAdminType).String())
	require.Equal(t, "admin", OrganizationRelations.Admin)
	require.Equal(t, "can_admin", OrganizationPermissions.CanAdmin)
	require.Error(t, Organization("").Filter().Validate(), "an empty ID must not widen to the whole type")
	require.Equal(t, "invitation:inv#inviter@user:alice#...", Invitation("inv").InviterKey(User("alice")).Filter().String())
}

func TestByNameLookups(t *testing.T) {
	org := Organization("acme")
	p, err := org.PermissionByName("can_admin")
	require.NoError(t, err)
	require.Equal(t, org.CanAdmin(), p)
	_, err = org.PermissionByName("admin") // a relation, not a permission
	require.ErrorIs(t, err, ErrNotInSchema)

	r, err := org.RelationByName("member")
	require.NoError(t, err)
	require.Equal(t, org.MemberRelation(), r)

	typed, err := org.RelationshipByName("creator", UserWildcard())
	require.NoError(t, err)
	require.Equal(t, "organization:acme#creator@user:*", typed.String())
	_, err = org.RelationshipByName("creator", User("alice"))
	require.ErrorIs(t, err, ErrSubjectNotAllowed)
	_, err = org.RelationshipByName("nope", User("alice"))
	require.ErrorIs(t, err, ErrNotInSchema)

	require.Equal(t, []string{"can_admin", "view"}, OrganizationPermissionNames())
	require.Equal(t, []string{"admin", "member", "creator", "guest"}, OrganizationRelationNames())
}

func TestAdoptionAgainstServer(t *testing.T) {
	ctx := context.Background()
	tc := newTestClient(t)
	_, err := tc.WriteSchema(ctx, client.WriteSchemaRequest{Schema: schema})
	require.NoError(t, err)
	cs := consistency.Full()
	acme, inv := Organization("acme-adoption"), Invitation("inv-adoption")

	// Clean slate; earlier runs may have left relationships behind.
	for _, f := range []rel.Filter{acme.Filter(), inv.Filter()} {
		_, err := tc.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: f})
		require.NoError(t, err)
	}

	// G1 + T6: permission userset and wildcard subjects write and check.
	_, err = tc.WriteTxn(ctx, (&Txn{}).
		Touch(acme.Admin(User("alice"))).
		Touch(acme.Member(User("bob"))).
		Touch(acme.Creator(UserWildcard())).
		Touch(acme.Guest(UserWildcard())).
		Touch(inv.Grant(acme.CanAdminSubject())).
		Touch(inv.TimedGrant(acme.CanAdminSubject().WithExpiration(time.Now().Add(time.Hour)))).
		Touch(inv.Inviter(User("alice").WithExpiration(time.Now().Add(time.Hour)))))
	require.NoError(t, err)

	accept, err := Check(ctx, tc, cs, inv.Accept(), User("alice"))
	require.NoError(t, err)
	require.True(t, accept.HasPermission(), "alice can accept through organization#can_admin")
	guest, err := Check(ctx, tc, cs, acme.View(), User("anyone"))
	require.NoError(t, err)
	require.True(t, guest.HasPermission(), "user:* guest grants view to anyone")

	// T8: relation checks.
	member, err := Check(ctx, tc, cs, acme.MemberRelation(), User("bob"))
	require.NoError(t, err)
	require.True(t, member.HasPermission())
	member, err = Check(ctx, tc, cs, acme.MemberRelation(), User("alice"))
	require.NoError(t, err)
	require.False(t, member.HasPermission(), "admin is not the member relation")

	// T4: typed bulk check, input order preserved.
	results, err := CheckBulk(ctx, tc, cs, ChecksFor(User("bob"), acme.CanAdmin(), acme.View(), acme.MemberRelation()))
	require.NoError(t, err)
	require.Len(t, results, 3)
	require.False(t, results[0].HasPermission())
	require.True(t, results[1].HasPermission())
	require.True(t, results[2].HasPermission())

	// T2: a precondition guards a mixed delete+touch; the failing guard writes nothing.
	_, err = tc.WriteTxn(ctx, (&Txn{}).
		MustNotMatch(WithSubject(acme.AdminFilter(), User("bob"))).
		Delete(acme.Member(User("bob"))).
		Touch(acme.Admin(User("bob"))))
	require.NoError(t, err)
	_, err = tc.WriteTxn(ctx, (&Txn{}).
		MustNotMatch(WithSubject(acme.AdminFilter(), User("bob"))).
		Touch(acme.Member(User("carol"))))
	require.ErrorIs(t, err, client.ErrFailedPrecondition)

	// T3 + T5: read back with a typed filter and delete what was read.
	var admins []string
	txn := &Txn{}
	for r, err := range tc.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: acme.AdminFilter(), Consistency: cs}) {
		require.NoError(t, err)
		admins = append(admins, r.SubjectID())
		txn.Delete(FromRelationship(r.Relationship))
	}
	require.ElementsMatch(t, []string{"alice", "bob"}, admins)
	_, err = tc.WriteTxn(ctx, txn)
	require.NoError(t, err)

	// T7: delete an expiration-only relationship by identity alone.
	_, err = tc.WriteTxn(ctx, (&Txn{}).Delete(inv.InviterKey(User("alice"))))
	require.NoError(t, err)
	for _, err := range tc.ReadRelationships(ctx, client.ReadRelationshipsRequest{Filter: inv.InviterFilter(), Consistency: cs}) {
		require.NoError(t, err)
		t.Fatal("inviter relationship should be deleted")
	}
}
