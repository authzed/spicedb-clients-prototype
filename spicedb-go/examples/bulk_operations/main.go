// Example bulk_operations demonstrates bulk checks, batch writes, and bulk
// import/export of relationships.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"slices"

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

	// Setup: write schema
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

	// Bulk write relationships
	var txn rel.Txn
	users := []string{"alice", "bob", "charlie"}
	for _, user := range users {
		if err := txn.Touch(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "report"), "viewer", exampleutil.Subject(exampleutil.Object("user", user), "")))); err != nil {
			log.Fatalf("failed to add relationship to transaction: %v", err)
		}
	}
	revision, err := c.Write(ctx, client.WriteRequest{Transaction: txn})
	if err != nil {
		log.Fatalf("bulk write failed: %v", err)
	}
	fmt.Printf("wrote %d relationships at revision: %s\n", len(users), revision.Revision)
	if revision.Revision.String() == "" {
		log.Fatalf("expected non-empty revision")
	}

	// Bulk check permissions. Inputs over 1,000 relationships are split
	// into one CheckBulkPermissions request per 1,000 automatically and
	// the results concatenated in input order -- SpiceDB rejects a single
	// request carrying more than 10,000. Nothing here changes for a
	// larger slice.
	checks := make([]client.PermissionCheck, len(users))
	for i, user := range users {
		checks[i] = client.PermissionCheck{Resource: exampleutil.Object("document", "report"), Permission: "view", Subject: exampleutil.Subject(exampleutil.Object("user", user), ""), Consistency: consistency.AtLeast(revision.Revision)}
	}
	results, err := c.CheckBulk(ctx, checks)
	if err != nil {
		log.Fatalf("bulk check failed: %v", err)
	}

	for i, user := range users {
		fmt.Printf("user:%s can view document:report: %v (permissionship: %s)\n", user, results[i].HasPermission(), results[i].Permissionship)
		if !results[i].HasPermission() {
			log.Fatalf("expected user:%s to have view permission", user)
		}
	}

	// Aggregate only unconditional grants. Empty input never grants.
	allAllowed, anyAllowed := len(results) > 0, false
	for _, result := range results {
		allAllowed = allAllowed && result.HasPermission()
		anyAllowed = anyAllowed || result.HasPermission()
	}
	if !allAllowed || !anyAllowed {
		log.Fatal("expected every bulk check to grant")
	}
	fmt.Printf("all users can view: %v; any user can view: %v\n", allAllowed, anyAllowed)

	// Bulk import relationships. ImportRelationships streams relationships
	// from an iter.Seq, batching them automatically.
	imported := []rel.Relationship{
		exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "bulkdoc1"), "viewer", exampleutil.Subject(exampleutil.Object("user", "dave"), ""))),
		exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "bulkdoc2"), "viewer", exampleutil.Subject(exampleutil.Object("user", "erin"), ""))),
		exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "bulkdoc3"), "viewer", exampleutil.Subject(exampleutil.Object("user", "frank"), ""))),
	}
	numLoaded, err := c.ImportRelationships(ctx, client.ImportRelationshipsRequest{Relationships: slices.Values(imported)})
	if err != nil {
		log.Fatalf("import relationships failed: %v", err)
	}
	fmt.Printf("imported %d relationships\n", numLoaded.ImportedCount)
	if numLoaded.ImportedCount != uint64(len(imported)) {
		log.Fatalf("expected %d relationships imported, got %d", len(imported), numLoaded.ImportedCount)
	}

	// Bulk export relationships. ExportRelationships returns an iterator that
	// transparently pages through all matching relationships.
	exportFilter := exampleutil.Must(rel.NewFilter("document")).WithResourceIDPrefix("bulkdoc")
	exportCount := 0
	for r, err := range c.ExportRelationships(ctx, client.ExportRelationshipsRequest{Consistency: consistency.Full()}, client.WithExportRelationshipsFilter(exportFilter)) {
		if err != nil {
			log.Fatalf("export relationships failed: %v", err)
		}
		fmt.Printf("exported relationship: %s\n", r.String())
		exportCount++
	}
	if exportCount != len(imported) {
		log.Fatalf("expected %d exported relationships, got %d", len(imported), exportCount)
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: exampleutil.Must(rel.NewFilter("document"))}); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
