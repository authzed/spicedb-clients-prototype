// Example read_relationships demonstrates reading relationships with an iterator.
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

	// Setup: write schema and data
	_, err = c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`})
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "firstdoc"), "viewer", exampleutil.Subject(exampleutil.Object("user", "alice"), "")))); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "firstdoc"), "viewer", exampleutil.Subject(exampleutil.Object("user", "bob"), "")))); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, client.WriteRequest{Transaction: txn})
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Read relationships
	filter := exampleutil.Must(rel.NewFilter("document")).
		WithResourceID("firstdoc").
		WithRelation("viewer")

	count := 0
	for r, err := range c.ReadRelationships(ctx, client.ReadRelationshipsRequest{Consistency: consistency.Full(), Filter: filter}) {
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
	if _, err := c.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: exampleutil.Must(rel.NewFilter("document"))}); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
