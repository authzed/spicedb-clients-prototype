// Example bulk_operations demonstrates bulk checks, batch writes, and bulk
// import/export of relationships.
package main

import (
	"context"
	"fmt"
	"log"
	"slices"

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

	// Setup: write schema
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

	// Bulk write relationships
	var txn rel.Txn
	users := []string{"alice", "bob", "charlie"}
	for _, user := range users {
		txn.Touch(rel.MustFromTriple("document", "report", "viewer", "user", user, ""))
	}
	revision, err := c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("bulk write failed: %v", err)
	}
	fmt.Printf("wrote %d relationships at revision: %s\n", len(users), revision)
	if revision == "" {
		log.Fatalf("expected non-empty revision")
	}

	// Bulk check permissions
	var checks []rel.Relationship
	for _, user := range users {
		checks = append(checks, rel.MustFromTriple("document", "report", "view", "user", user, ""))
	}

	results, err := c.Check(ctx, consistency.AtLeast(revision), "view", checks)
	if err != nil {
		log.Fatalf("bulk check failed: %v", err)
	}

	for i, user := range users {
		fmt.Printf("user:%s can view document:report: %v (permissionship: %s)\n", user, results[i].HasPermission(), results[i].Permissionship)
		if !results[i].HasPermission() {
			log.Fatalf("expected user:%s to have view permission", user)
		}
	}

	// CheckAll
	allAllowed, err := c.CheckAll(ctx, consistency.AtLeast(revision), "view", checks)
	if err != nil {
		log.Fatalf("check all failed: %v", err)
	}
	fmt.Printf("all users can view: %v\n", allAllowed)
	if !allAllowed {
		log.Fatalf("expected all users to have view permission")
	}

	// CheckAny
	anyAllowed, err := c.CheckAny(ctx, consistency.AtLeast(revision), "view", checks)
	if err != nil {
		log.Fatalf("check any failed: %v", err)
	}
	fmt.Printf("any user can view: %v\n", anyAllowed)
	if !anyAllowed {
		log.Fatalf("expected at least one user to have view permission")
	}

	// Bulk import relationships. ImportRelationships streams relationships
	// from an iter.Seq, batching them automatically.
	imported := []rel.Relationship{
		rel.MustFromTriple("document", "bulkdoc1", "viewer", "user", "dave", ""),
		rel.MustFromTriple("document", "bulkdoc2", "viewer", "user", "erin", ""),
		rel.MustFromTriple("document", "bulkdoc3", "viewer", "user", "frank", ""),
	}
	numLoaded, err := c.ImportRelationships(ctx, slices.Values(imported))
	if err != nil {
		log.Fatalf("import relationships failed: %v", err)
	}
	fmt.Printf("imported %d relationships\n", numLoaded)
	if numLoaded != uint64(len(imported)) {
		log.Fatalf("expected %d relationships imported, got %d", len(imported), numLoaded)
	}

	// Bulk export relationships. ExportRelationships returns an iterator that
	// transparently pages through all matching relationships.
	exportFilter := rel.NewFilter("document").WithResourceIDPrefix("bulkdoc")
	exportCount := 0
	for r, err := range c.ExportRelationships(ctx, consistency.Full(), &exportFilter) {
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
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
