// Example delete_relationships demonstrates deleting relationships,
// including a precondition-guarded delete.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
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

	// Guarded delete: only remove the viewers if the document still has an
	// owner. WithDeleteMustMatch adds a precondition that fails (rejecting the
	// whole delete) unless a relationship matching ownerGuard exists at
	// evaluation time. This guards against accidentally stripping viewers from
	// a document that has already lost its owner.
	viewerFilter := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("viewer")
	ownerGuard := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner")

	revision, err := c.DeleteRelationships(ctx, viewerFilter, client.WithDeleteMustMatch(ownerGuard))
	if err != nil {
		log.Fatalf("guarded delete failed: %v", err)
	}
	fmt.Printf("deleted viewers at revision: %s\n", revision)

	// A delete guarded by a precondition that ISN'T satisfied is rejected
	// outright rather than silently deleting nothing.
	ownerFilter := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner")
	neverMatches := rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("viewer").
		WithSubjectType("user").WithSubjectID("nonexistent-subject")

	_, err = c.DeleteRelationships(ctx, ownerFilter, client.WithDeleteMustMatch(neverMatches))
	if err == nil {
		log.Fatalf("expected guarded delete with unsatisfied precondition to fail")
	}
	fmt.Printf("guarded delete correctly rejected: %v\n", err)

	// WithDeleteLimit overrides the default 1,000-per-call page size used by
	// DeleteRelationships' auto-paging loop.
	revision, err = c.DeleteRelationships(ctx, ownerFilter, client.WithDeleteLimit(1))
	if err != nil {
		log.Fatalf("delete with limit failed: %v", err)
	}
	fmt.Printf("deleted owner at revision: %s\n", revision)
}
