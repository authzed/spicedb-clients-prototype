// Example check_permission demonstrates checking a permission using CheckOne.
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

	// Setup: write schema and test data
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
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Check permission
	r := rel.MustFromTriple("document", "firstdoc", "view", "user", "alice", "")

	allowed, err := c.CheckOne(ctx, consistency.Full(), "view", r)
	if err != nil {
		log.Fatalf("check failed: %v", err)
	}

	fmt.Printf("alice can view document:firstdoc: %v\n", allowed)
	if !allowed {
		log.Fatalf("expected alice to have view permission")
	}
}
