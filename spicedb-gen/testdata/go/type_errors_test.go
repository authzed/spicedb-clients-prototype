//go:build typeerror

package permissions_test

import (
	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
)

// This file intentionally does not compile. It is used to verify that
// the generated code's type constraints correctly reject invalid usage.
// The Magefile attempts to build this file and asserts that it fails.

func typeErrors() {
	// editor only accepts UserRef (DocumentEditorSubject), not TeamMemberRef
	_ = Document("x").Editor(TeamMember("eng")) // should not compile

	// editor does not accept caveated user (UserIpRange is not a DocumentEditorSubject)
	_ = Document("x").Editor(User("x").WithIpRange(IpRangeContext{})) // should not compile
}
