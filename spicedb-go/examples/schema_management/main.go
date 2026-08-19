// Example schema_management demonstrates reading and writing schema.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
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

	// Write a schema
	schema := `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`

	revision, err := c.WriteSchema(ctx, schema)
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}
	fmt.Printf("wrote schema at revision: %s\n", revision)
	if revision == "" {
		log.Fatalf("expected non-empty revision")
	}

	// Read the schema back
	readSchema, readRevision, err := c.ReadSchema(ctx)
	if err != nil {
		log.Fatalf("read schema failed: %v", err)
	}
	fmt.Printf("read schema at revision %s:\n%s\n", readRevision, readSchema)

	if !strings.Contains(readSchema, "definition user") {
		log.Fatalf("expected schema to contain 'definition user'")
	}
	if !strings.Contains(readSchema, "definition document") {
		log.Fatalf("expected schema to contain 'definition document'")
	}
}
