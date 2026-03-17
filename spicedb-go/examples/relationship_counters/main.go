// Example relationship_counters demonstrates registering, reading, and
// unregistering relationship counters.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
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
	txn.Touch(rel.MustFromTriple("document", "seconddoc", "viewer", "user", "bob", ""))
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Unregister any existing counter from a prior run (ignore errors)
	filter := rel.NewFilter("document").WithRelation("viewer")
	_ = c.UnregisterRelationshipCounter(ctx, "document_viewers")

	// Register a counter for all document viewer relationships
	err = c.RegisterRelationshipCounter(ctx, "document_viewers", filter)
	if err != nil {
		log.Fatalf("register counter failed: %v", err)
	}
	fmt.Println("registered counter: document_viewers")

	// Wait a moment for the counter to be computed
	time.Sleep(2 * time.Second)

	// Read the counter value
	result, stillCalculating, err := c.CountRelationships(ctx, "document_viewers")
	if err != nil {
		log.Fatalf("count relationships failed: %v", err)
	}
	if stillCalculating {
		fmt.Println("counter is still being calculated...")
	} else {
		fmt.Printf("document viewer count: %d (revision: %s)\n",
			result.RelationshipCount, result.Revision)
		if result.RelationshipCount == 0 {
			log.Fatalf("expected non-zero relationship count")
		}
	}

	// Unregister the counter when done
	err = c.UnregisterRelationshipCounter(ctx, "document_viewers")
	if err != nil {
		log.Fatalf("unregister counter failed: %v", err)
	}
	fmt.Println("unregistered counter: document_viewers")
}
