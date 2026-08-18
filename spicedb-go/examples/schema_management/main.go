// Example schema_management demonstrates reading and writing schema.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer c.Close()

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
