// Example check_permission demonstrates checking a permission using PermissionCheck and Check,
// including the three-valued CheckResult returned for a caveated
// relationship whose context was not supplied at check time.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/authzed/spicedb-clients/spicedb-go/examples/internal/exampleutil"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	// Endpoint and token come from the environment so the example runs against
	// whichever SpiceDB the caller started; the defaults match
	// docker-compose.test.yml.
	c, err := client.NewPlaintext(
		cmp.Or(os.Getenv("SPICEDB_ENDPOINT"), "localhost:50051"),
		cmp.Or(os.Getenv("SPICEDB_TOKEN"), "somerandomkeyhere"),
	)
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	// Setup: write schema and test data. The "active" caveat and
	// conditional_viewer relation exist to demonstrate a Conditional check
	// result below.
	_, err = c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: `definition user {}

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
}`})
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "firstdoc"), "viewer", exampleutil.Subject(exampleutil.Object("user", "alice"), "")))); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, client.WriteRequest{Transaction: txn})
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// A permission question carries its own context and explicit consistency.
	document := exampleutil.Object("document", "firstdoc")
	alice := exampleutil.Subject(exampleutil.Object("user", "alice"), "")
	request := client.PermissionCheck{Resource: document, Permission: "view", Subject: alice, Consistency: consistency.Full()}
	result, err := c.Check(ctx, request, client.WithCheckTracing(client.TracingEnabled))
	if err != nil {
		log.Fatalf("check failed: %v", err)
	}

	fmt.Printf("alice can view document:firstdoc: %v (permissionship: %s)\n", result.HasPermission(), result.Permissionship)
	if result.DebugTrace == nil || result.DebugTrace.Check == nil || result.DebugTrace.SchemaUsed == "" {
		log.Fatal("expected populated native check trace and schema")
	}
	if !result.HasPermission() {
		log.Fatalf("expected alice to have view permission")
	}

	// CheckedAt is the revision this check was evaluated at. Thread it into
	// consistency.AtLeast to make a later read observe this check (and
	// everything it observed) — read-your-writes for checks.
	if result.CheckedAt.String() == "" {
		log.Fatalf("expected a non-empty CheckedAt token")
	}

	// Write a caveated relationship without supplying its context, then check
	// it with no context either. The server cannot evaluate "now < 100"
	// without "now", so it reports Conditional rather than guessing — and
	// HasPermission() MUST be false for a Conditional result, or callers would
	// be granting access on an unevaluated condition.
	var caveatedTxn rel.Txn
	if err := caveatedTxn.Touch(exampleutil.Must(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "conditionaldoc"), "conditional_viewer", exampleutil.Subject(exampleutil.Object("user", "alice"), ""))).WithCaveat("active", nil))); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if _, err := c.Write(ctx, client.WriteRequest{Transaction: caveatedTxn}); err != nil {
		log.Fatalf("write caveated relationship failed: %v", err)
	}

	conditionalRequest := client.PermissionCheck{Resource: exampleutil.Object("document", "conditionaldoc"), Subject: alice, Permission: "conditional_view", Consistency: consistency.Full()}
	condResult, err := c.Check(ctx, conditionalRequest)
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

	// Now supply the context the server said was missing. request.Context
	// puts "now" into the check request (not into the stored relationship —
	// the relationship was written above with a nil caveat context and stays
	// that way). "now" (50) satisfies the caveat's "now < 100", so the same
	// check that came back Conditional above now resolves to a real grant:
	// this is the payoff of MissingContext being actionable instead of just
	// informational.
	conditionalRequest.Context = map[string]any{"now": 50}
	resolvedResult, err := c.Check(ctx, conditionalRequest)
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
	if _, err := c.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: exampleutil.Must(rel.NewFilter("document"))}); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
