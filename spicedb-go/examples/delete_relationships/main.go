// Example delete_relationships demonstrates deleting relationships,
// including a precondition-guarded delete.
//
// Every delete here is read back. "The call returned no error" is not evidence
// that anything was deleted -- nor, for the guarded delete that must be
// rejected, that nothing was. See root DESIGN.md, "RULE: An example must be
// executed by CI and must be able to fail", clause 2.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"

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

	// Setup: write schema
	_, err = c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`)
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	// Start from a known state so the counts read back below are this
	// example's own writes and not an earlier example's leftovers.
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	// Write relationships to delete
	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "owner", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "carol", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if _, err := c.Write(ctx, txn); err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	viewerFilter := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("viewer")
	ownerFilter := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner")

	if n := count(ctx, c, viewerFilter); n != 2 {
		log.Fatalf("expected 2 viewers before the delete, got %d", n)
	}

	// Guarded delete: only remove the viewers if the document still has an
	// owner. WithDeleteMustMatch adds a precondition that fails (rejecting the
	// whole delete) unless a relationship matching ownerGuard exists at
	// evaluation time. This guards against accidentally stripping viewers from
	// a document that has already lost its owner.
	ownerGuard := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner")

	revision, err := c.DeleteRelationships(ctx, viewerFilter, client.WithDeleteMustMatch(ownerGuard))
	if err != nil {
		log.Fatalf("guarded delete failed: %v", err)
	}
	fmt.Printf("deleted viewers at revision: %s\n", revision)

	// Read the delete back. A DeleteRelationships that built the wrong filter,
	// or dropped it entirely, returns a revision and no error either way.
	if n := countAt(ctx, c, consistency.AtLeast(revision), viewerFilter); n != 0 {
		log.Fatalf("expected every viewer to be gone after the guarded delete, %d remain", n)
	}
	if n := countAt(ctx, c, consistency.AtLeast(revision), ownerFilter); n != 1 {
		log.Fatalf("expected the owner to survive a delete filtered to viewers, got %d owners", n)
	}

	// A delete guarded by a precondition that ISN'T satisfied is rejected
	// outright rather than silently deleting nothing.
	neverMatches := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("viewer").
		WithSubjectType("user").WithSubjectID("nonexistent-subject")

	_, err = c.DeleteRelationships(ctx, ownerFilter, client.WithDeleteMustMatch(neverMatches))
	if err == nil {
		log.Fatalf("expected guarded delete with unsatisfied precondition to fail")
	}
	fmt.Printf("guarded delete correctly rejected: %v\n", err)

	// The rejection arrives as this client's own typed error, with SpiceDB's
	// structured explanation attached -- not as a bare "something went wrong".
	// Asserting only `err != nil` would pass on an UNAUTHENTICATED, an
	// InvalidArgument from a malformed filter, or a connection failure.
	if !errors.Is(err, client.ErrFailedPrecondition) {
		log.Fatalf("expected client.ErrFailedPrecondition, got: %v", err)
	}
	var spiceErr *client.Error
	if !errors.As(err, &spiceErr) {
		log.Fatalf("expected a native *client.Error, got: %v", err)
	}
	if spiceErr.Reason != "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE" {
		log.Fatalf("expected the write/delete precondition reason, got: %q", spiceErr.Reason)
	}

	// "Rejected" has to mean nothing was deleted, which is the whole point of
	// guarding the delete.
	if n := count(ctx, c, ownerFilter); n != 1 {
		log.Fatalf("expected the rejected delete to leave the owner in place, got %d owners", n)
	}

	// WithDeleteLimit overrides the default 1,000-per-call page size used by
	// DeleteRelationships' auto-paging loop. Two more owners are written first
	// so a limit of 1 forces three separate server calls: if the auto-paging
	// loop stopped after the first page, the read-back below would still find
	// two owners.
	var more rel.Txn
	if err := more.Touch(rel.MustFromTriple("document", "firstdoc", "owner", "user", "dave", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := more.Touch(rel.MustFromTriple("document", "firstdoc", "owner", "user", "erin", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if _, err := c.Write(ctx, more); err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}
	if n := count(ctx, c, ownerFilter); n != 3 {
		log.Fatalf("expected 3 owners before the limited delete, got %d", n)
	}

	revision, err = c.DeleteRelationships(ctx, ownerFilter, client.WithDeleteLimit(1))
	if err != nil {
		log.Fatalf("delete with limit failed: %v", err)
	}
	fmt.Printf("deleted owners one page at a time, through revision: %s\n", revision)

	if n := countAt(ctx, c, consistency.AtLeast(revision), ownerFilter); n != 0 {
		log.Fatalf("expected the paged delete to remove every owner, %d remain", n)
	}
}

// count reads back how many relationships match filter, at full consistency.
func count(ctx context.Context, c *client.Client, filter rel.Filter) int {
	return countAt(ctx, c, consistency.Full(), filter)
}

// countAt reads back how many relationships match filter at the given
// consistency. Reading at least as fresh as the revision a delete returned is
// what makes the read-back a proof rather than a race.
func countAt(ctx context.Context, c *client.Client, cs consistency.Strategy, filter rel.Filter) int {
	n := 0
	for r, err := range c.ReadRelationships(ctx, cs, filter) {
		if err != nil {
			log.Fatalf("read relationships failed: %v", err)
		}
		_ = r
		n++
	}
	return n
}
