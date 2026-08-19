// Example write_relationships demonstrates writing relationships with a
// transaction builder.
package main

import (
	"context"
	"errors"
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

	// Write relationships with transaction builder
	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "editor", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.MustNotMatch(rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner").WithSubjectType("user").WithSubjectID("mallory")); err != nil {
		log.Fatalf("failed to add precondition to transaction: %v", err)
	}

	revision, err := c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write failed: %v", err)
	}

	fmt.Printf("wrote relationships at revision: %s\n", revision)
	if revision == "" {
		log.Fatalf("expected non-empty revision")
	}

	// A failed precondition arrives with SpiceDB's structured explanation
	// attached, not just a message. The typed error exposes the
	// authzed.api.v1.ErrorReason name and the metadata naming which
	// precondition did not hold, so recovery can be written against data
	// rather than against a parsed string.
	var doomed rel.Txn
	if err := doomed.Touch(rel.MustFromTriple("document", "seconddoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := doomed.MustMatch(rel.NewFilter("document").WithResourceID("firstdoc").WithRelation("owner").WithSubjectType("user").WithSubjectID("nobody")); err != nil {
		log.Fatalf("failed to add precondition to transaction: %v", err)
	}

	if _, err := c.Write(ctx, doomed); err == nil {
		log.Fatalf("expected the unsatisfiable precondition to fail the write")
	} else {
		if !errors.Is(err, client.ErrFailedPrecondition) {
			log.Fatalf("expected client.ErrFailedPrecondition, got: %v", err)
		}
		var spiceErr *client.Error
		if errors.As(err, &spiceErr) {
			fmt.Printf("precondition failed: reason=%q domain=%q metadata=%v\n",
				spiceErr.Reason, spiceErr.ReasonDomain, spiceErr.ReasonMetadata)
		}
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
