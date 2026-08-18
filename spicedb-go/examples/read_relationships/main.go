// Example read_relationships demonstrates reading relationships with an iterator.
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

	// Setup: write schema and data
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

	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Read relationships
	filter := rel.NewFilter("document").
		WithResourceID("firstdoc").
		WithRelation("viewer")

	count := 0
	for r, err := range c.ReadRelationships(ctx, consistency.Full(), filter) {
		if err != nil {
			log.Fatalf("read failed: %v", err)
		}
		fmt.Printf("found relationship: %s\n", r.String())
		count++
	}

	if count == 0 {
		log.Fatalf("expected at least one relationship, got none")
	}
	fmt.Printf("found %d relationships\n", count)

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
