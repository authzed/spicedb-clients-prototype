// Example lookup_resources demonstrates finding resources a subject can
// access, including how to read the Permissionship of each result. A
// Permissionship of PermissionshipConditionalPermission means the match
// depends on caveat context that wasn't supplied — callers must not treat
// it as an unconditional grant.
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
	txn.Touch(rel.MustFromTriple("document", "seconddoc", "editor", "user", "alice", ""))
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Lookup resources
	found := map[string]bool{}
	for resource, err := range c.LookupResources(ctx, consistency.Full(), "document", "view", "user", "alice") {
		if err != nil {
			log.Fatalf("lookup failed: %v", err)
		}
		fmt.Printf("alice can view document:%s (permissionship=%s)\n", resource.ResourceID, resource.Permissionship)
		if resource.Permissionship != client.PermissionshipHasPermission {
			// A conditional result means caveat context is missing; PartialCaveat
			// lists which context. Never treat a conditional result as a full
			// grant.
			log.Fatalf("unexpected permissionship for document:%s: %s (missing context: %v)", resource.ResourceID, resource.Permissionship, resource.PartialCaveat)
		}
		found[resource.ResourceID] = true
	}

	if !found["firstdoc"] {
		log.Fatalf("expected firstdoc in results")
	}
	if !found["seconddoc"] {
		log.Fatalf("expected seconddoc in results")
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
