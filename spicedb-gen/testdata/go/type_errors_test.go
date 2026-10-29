//go:build typeerror

package permissions_test

import (
	"context"
	"time"

	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// This file intentionally does not compile. It is used to verify that
// the generated code's type constraints correctly reject invalid usage.
// The Magefile builds it with every error reported (-gcflags=-e) and asserts
// that each line marked "should not compile" fails on its own.

func typeErrors() {
	_ = consistency.AtLeast("plain string")         // should not compile
	_ = client.WithCheckTracing(true)               // should not compile
	_ = client.WithRemotePlaintextCredentials(true) // should not compile
	// editor only accepts UserRef (DocumentEditorSubject), not TeamMemberRef
	_ = Document("x").Editor(Team("eng").Member()) // should not compile

	// editor does not accept caveated user (UserIpRange is not a DocumentEditorSubject)
	_ = Document("x").Editor(User("x").WithIpRange(IpRangeContext{})) // should not compile

	// creator admits only user:*, never a concrete user
	_ = Organization("x").Creator(User("alice")) // should not compile
	// grant admits only organization#can_admin, a permission userset
	_ = Invitation("x").Grant(User("alice"))                               // should not compile
	_ = Invitation("x").Grant(Organization("acme"))                        // should not compile
	_ = Invitation("x").TimedGrant(Organization("acme").CanAdminSubject()) // should not compile
	// inviter admits only expiring users; its key admits only the base user
	_ = Invitation("x").Inviter(User("alice"))                                // should not compile
	_ = Invitation("x").InviterKey(User("alice").WithExpiration(time.Time{})) // should not compile
	// CanAdmin stays the permission accessor; the subject accessor is CanAdminSubject
	_ = Invitation("x").Grant(Organization("acme").CanAdmin()) // should not compile
	// WriteTxn does not shadow the native Write
	_, _ = NewTypedClient(nil).Write(context.Background(), &Txn{}) // should not compile

	// NewTypedClient takes a client.Interface, not an endpoint or a bare value
	_ = NewTypedClient("localhost:50051") // should not compile
	_ = NewTypedClient(struct{}{})        // should not compile
	// By-name lookups report schema errors; their results cannot be used unchecked
	var _ PermissionRef = PermissionRefByName("document", "view") // should not compile
	var _ Definition = DefinitionByName("document")               // should not compile
	// A Definition is not a PermissionRef, and only the schema builds one
	var _ PermissionRef = Definition{}          // should not compile
	_ = Definition{name: "document"}            // should not compile
	_ = Definitions.Folder                      // should not compile
	SchemaText = "definition folder {}"         // should not compile
	_ = DefinitionNames()[0] + Definitions.User // fine: both are plain strings
	// LookupResourcesByName takes a typed subject, not a tuple string
	_ = LookupResourcesByName(context.Background(), NewTypedClient(nil), consistency.Full(), "document", "view", "user:alice") // should not compile
}
