// Example schema_management demonstrates reading and writing schema.
//
// The assertions here are deliberately about *this example's own* schema
// change. Asserting that the schema read back contains "definition user" would
// pass no matter what this example did: all thirteen Go examples write a
// schema containing `definition user` into the same shared SpiceDB, so that
// string is there whether or not ReadSchema reflects the write above it.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// The two schemas differ only in the name of one relation. Round-tripping the
// difference is what proves ReadSchema returns the current schema rather than
// a cached or constant one.
const (
	schemaWithAuditor = `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	relation auditor: user
	permission view = viewer + editor + owner + auditor
	permission edit = editor + owner
	permission delete = owner
}`

	schemaWithArchivist = `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	relation archivist: user
	permission view = viewer + editor + owner + archivist
	permission edit = editor + owner
	permission delete = owner
}`

	// What the other examples expect to find when they run after this one.
	sharedSchema = `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`
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

	// SpiceDB refuses a WriteSchema that drops a relation while a relationship
	// still exists under it, and this example replaces one relation with
	// another twice. Clear first so the rewrite below is about schema, not
	// about what an earlier example left behind.
	if _, err := c.WriteSchema(ctx, sharedSchema); err != nil {
		log.Fatalf("write schema failed: %v", err)
	}
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	// Write a schema
	revision, err := c.WriteSchema(ctx, schemaWithAuditor)
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
	if readRevision == "" {
		log.Fatalf("expected a non-empty revision from ReadSchema")
	}
	// Both definitions round-trip. These two are weak on their own -- every
	// example writes both strings into the same shared SpiceDB -- which is why
	// the relation-level assertions below exist; they are kept because they
	// are still true and still cheap.
	if !strings.Contains(readSchema, "definition user") {
		log.Fatalf("expected schema to contain 'definition user'")
	}
	if !strings.Contains(readSchema, "definition document") {
		log.Fatalf("expected schema to contain 'definition document'")
	}
	if !strings.Contains(readSchema, "relation auditor") {
		log.Fatalf("expected the schema just written to come back with `relation auditor`, got:\n%s", readSchema)
	}
	if strings.Contains(readSchema, "relation archivist") {
		log.Fatalf("`relation archivist` has not been written yet, but ReadSchema returned it:\n%s", readSchema)
	}

	// Rewrite the schema, changing exactly one relation, and read it back
	// again. The second read is what distinguishes "ReadSchema returns the
	// current schema" from "ReadSchema returns something schema-shaped".
	if _, err := c.WriteSchema(ctx, schemaWithArchivist); err != nil {
		log.Fatalf("second write schema failed: %v", err)
	}

	rewritten, rewrittenRevision, err := c.ReadSchema(ctx)
	if err != nil {
		log.Fatalf("second read schema failed: %v", err)
	}
	if !strings.Contains(rewritten, "relation archivist") {
		log.Fatalf("expected the rewritten schema to contain `relation archivist`, got:\n%s", rewritten)
	}
	if strings.Contains(rewritten, "relation auditor") {
		log.Fatalf("`relation auditor` was replaced, but ReadSchema still returns it:\n%s", rewritten)
	}
	if rewrittenRevision == readRevision {
		log.Fatalf("expected the revision to advance across a schema rewrite, both reads returned %s",
			readRevision)
	}
	fmt.Printf("schema rewrite visible at revision %s\n", rewrittenRevision)

	// Leave the shared schema in place for the examples that run after this
	// one (they all run in sequence against one SpiceDB).
	if _, err := c.WriteSchema(ctx, sharedSchema); err != nil {
		log.Fatalf("restoring the shared schema failed: %v", err)
	}
}
