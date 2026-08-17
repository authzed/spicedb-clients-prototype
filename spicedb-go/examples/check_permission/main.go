// Example check_permission demonstrates checking a permission using CheckOne,
// including the three-valued CheckResult returned for a caveated
// relationship whose context was not supplied at check time.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	ctx := context.Background()

	// Setup: write schema and test data. The "active" caveat and
	// conditional_viewer relation exist to demonstrate a Conditional check
	// result below.
	_, err = c.WriteSchema(ctx, `definition user {}

caveat active(now int) {
	now < 100
}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	relation conditional_viewer: user with active
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
	permission conditional_view = conditional_viewer
}`)
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	var txn rel.Txn
	txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", ""))
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Check permission
	r := rel.MustFromTriple("document", "firstdoc", "view", "user", "alice", "")

	result, err := c.CheckOne(ctx, consistency.Full(), "view", r)
	if err != nil {
		log.Fatalf("check failed: %v", err)
	}

	fmt.Printf("alice can view document:firstdoc: %v (permissionship: %s)\n", result.HasPermission(), result.Permissionship)
	if !result.HasPermission() {
		log.Fatalf("expected alice to have view permission")
	}

	// CheckedAt is the revision this check was evaluated at. Thread it into
	// consistency.AtLeast to make a later read observe this check (and
	// everything it observed) — read-your-writes for checks.
	if result.CheckedAt == "" {
		log.Fatalf("expected a non-empty CheckedAt token")
	}

	// Write a caveated relationship without supplying its context, then check
	// it with no context either. The server cannot evaluate "now < 100"
	// without "now", so it reports Conditional rather than guessing — and
	// HasPermission() MUST be false for a Conditional result, or callers would
	// be granting access on an unevaluated condition.
	var caveatedTxn rel.Txn
	caveatedTxn.Touch(rel.MustFromTriple("document", "conditionaldoc", "conditional_viewer", "user", "alice", "").WithCaveat("active", nil))
	if _, err := c.Write(ctx, caveatedTxn); err != nil {
		log.Fatalf("write caveated relationship failed: %v", err)
	}

	condResult, err := c.CheckOne(ctx, consistency.Full(), "conditional_view",
		rel.MustFromTriple("document", "conditionaldoc", "conditional_view", "user", "alice", ""))
	if err != nil {
		log.Fatalf("conditional check failed: %v", err)
	}

	fmt.Printf("alice can conditionally view document:conditionaldoc: %v (permissionship: %s, missing context: %v)\n",
		condResult.HasPermission(), condResult.Permissionship, condResult.MissingContext)
	if condResult.Permissionship != client.PermissionshipConditionalPermission {
		log.Fatalf("expected ConditionalPermission with no caveat context supplied, got %s", condResult.Permissionship)
	}
	if condResult.HasPermission() {
		log.Fatalf("a Conditional result must never report HasPermission() == true")
	}
	if len(condResult.MissingContext) != 1 || condResult.MissingContext[0] != "now" {
		log.Fatalf(`expected MissingContext == ["now"], got %v`, condResult.MissingContext)
	}

	// Now supply the context the server said was missing. WithCheckContext
	// puts "now" into the check request (not into the stored relationship —
	// the relationship was written above with a nil caveat context and stays
	// that way). "now" (50) satisfies the caveat's "now < 100", so the same
	// check that came back Conditional above now resolves to a real grant:
	// this is the payoff of MissingContext being actionable instead of just
	// informational.
	resolvedResult, err := c.CheckOne(ctx, consistency.Full(), "conditional_view",
		rel.MustFromTriple("document", "conditionaldoc", "conditional_view", "user", "alice", ""),
		client.WithCheckContext(map[string]any{"now": 50}),
	)
	if err != nil {
		log.Fatalf("check with context failed: %v", err)
	}
	fmt.Printf("alice can view document:conditionaldoc with now=50 supplied: %v (permissionship: %s)\n",
		resolvedResult.HasPermission(), resolvedResult.Permissionship)
	if !resolvedResult.HasPermission() {
		log.Fatalf("expected supplying the missing caveat context to resolve the conditional check to a grant, got %s", resolvedResult.Permissionship)
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
