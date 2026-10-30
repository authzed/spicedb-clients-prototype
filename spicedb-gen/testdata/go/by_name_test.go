package permissions_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/clienttest"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

// These tests need no server: a policy layer that receives type and permission
// names at runtime stays typed, and a typed client wraps the test double.

func TestSchemaTextAndDefinitionNames(t *testing.T) {
	require.Equal(t, schema, SchemaText, "SchemaText is the schema the file was generated from")
	require.Equal(t, "document", Definitions.Document)
	require.Equal(t, []string{"user", "team", "document", "organization", "invitation"}, DefinitionNames())
	names := DefinitionNames()
	names[0] = "changed"
	require.Equal(t, "user", DefinitionNames()[0], "callers get a copy")
}

func TestDefinitionByName(t *testing.T) {
	d, err := DefinitionByName("document")
	require.NoError(t, err)
	require.Equal(t, "document", d.Name())
	require.Equal(t, DocumentPermissionNames(), d.PermissionNames())
	require.Equal(t, DocumentRelationNames(), d.RelationNames())

	ref, err := d.PermissionRef("view")
	require.NoError(t, err)
	require.Equal(t, Document_View, ref)

	p, err := d.Permission("readme", "edit")
	require.NoError(t, err)
	require.Equal(t, Document("readme").Edit(), p)

	r, err := d.Relation("readme", "viewer")
	require.NoError(t, err)
	require.Equal(t, Document("readme").ViewerRelation(), r)

	_, err = DefinitionByName("folder")
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = d.PermissionRef("viewer")
	require.ErrorIs(t, err, ErrNotInSchema, "a relation is not a permission")
	_, err = d.Permission("readme", "nope")
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = d.Relation("readme", "view")
	require.ErrorIs(t, err, ErrNotInSchema, "a permission is not a relation")
}

func TestPermissionRefByName(t *testing.T) {
	ref, err := PermissionRefByName("organization", "can_admin")
	require.NoError(t, err)
	require.Equal(t, Organization_CanAdmin, ref)
	require.Equal(t, "organization#can_admin", ref.String())

	_, err = PermissionRefByName("folder", "view")
	require.ErrorIs(t, err, ErrNotInSchema)
	_, err = PermissionRefByName("organization", "edit")
	require.ErrorIs(t, err, ErrNotInSchema)
}

func TestTypedClientWrapsTheTestDouble(t *testing.T) {
	ctx := context.Background()
	store := clienttest.NewStore(
		rel.MustFromTuple("document:readme#view@user:alice"),
		rel.MustFromTuple("document:plan#view@user:alice"),
		rel.MustFromTuple("document:secret#view@user:bob"),
	)
	tc := NewTypedClient(store)
	cs := consistency.Full()

	// Typed checks go through the double's check resolution.
	r, err := Check(ctx, tc, cs, Document("readme").View(), User("alice"))
	require.NoError(t, err)
	require.True(t, r.HasPermission())
	r, err = Check(ctx, tc, cs, Document("secret").View(), User("alice"))
	require.NoError(t, err)
	require.False(t, r.HasPermission())

	// Typed writes land in the double.
	_, err = tc.Touch(ctx, []TypedRelationship{Document("readme").Viewer(User("carol"))})
	require.NoError(t, err)
	require.Len(t, store.CallsOf(clienttest.OpWrite), 1)

	// Runtime-name lookups are validated, then served by the double.
	var ids []string
	for res, err := range LookupResourcesByName(ctx, tc, cs, "document", "view", User("alice")) {
		require.NoError(t, err)
		ids = append(ids, res.ResourceID)
	}
	require.Equal(t, []string{"readme", "plan"}, ids)
	for _, err := range LookupResourcesByName(ctx, tc, cs, "document", "nope", User("alice")) {
		require.ErrorIs(t, err, ErrNotInSchema)
	}
	require.Len(t, store.CallsOf(clienttest.OpLookupResources), 1, "an invalid name fails before any call")

	// A grant-all resolver answers generated IDs without registration.
	store.SetCheckResolver(clienttest.GrantAll)
	results, err := CheckBulk(ctx, tc, cs, ChecksFor(User("u-1"), Document("d-1").Delete(), Organization("o-1").CanAdmin()))
	require.NoError(t, err)
	require.True(t, results[0].HasPermission())
	require.True(t, results[1].HasPermission())

	// The native surface is the double's.
	_, err = tc.WriteSchema(ctx, client.WriteSchemaRequest{Schema: SchemaText})
	require.NoError(t, err)
	got, err := tc.ReadSchema(ctx, client.ReadSchemaRequest{})
	require.NoError(t, err)
	require.Equal(t, SchemaText, got.Schema)
}
