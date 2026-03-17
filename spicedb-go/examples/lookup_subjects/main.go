// Example lookup_subjects demonstrates finding subjects with access to a resource.
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
	txn.Touch(rel.MustFromTriple("document", "firstdoc", "editor", "user", "bob", ""))
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Lookup subjects
	found := map[string]bool{}
	for subjectID, err := range c.LookupSubjects(ctx, consistency.Full(), "document", "firstdoc", "view", "user") {
		if err != nil {
			log.Fatalf("lookup failed: %v", err)
		}
		fmt.Printf("user:%s can view document:firstdoc\n", subjectID)
		found[subjectID] = true
	}

	if !found["alice"] {
		log.Fatalf("expected alice in results")
	}
	if !found["bob"] {
		log.Fatalf("expected bob in results (editor implies view)")
	}
}
