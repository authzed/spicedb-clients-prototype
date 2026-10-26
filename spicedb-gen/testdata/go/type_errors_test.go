//go:build typeerror

package permissions_test

import (
	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// This file intentionally does not compile. It is used to verify that
// the generated code's type constraints correctly reject invalid usage.
// The Magefile attempts to build this file and asserts that it fails.

func typeErrors() {
	_ = consistency.AtLeast("plain string")         // should not compile
	_ = client.WithCheckTracing(true)               // should not compile
	_ = client.WithRemotePlaintextCredentials(true) // should not compile
	// editor only accepts UserRef (DocumentEditorSubject), not TeamMemberRef
	_ = Document("x").Editor(Team("eng").Member()) // should not compile

	// editor does not accept caveated user (UserIpRange is not a DocumentEditorSubject)
	_ = Document("x").Editor(User("x").WithIpRange(IpRangeContext{})) // should not compile
}
