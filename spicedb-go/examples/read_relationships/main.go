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
	txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", ""))
	txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "bob", ""))
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
}
