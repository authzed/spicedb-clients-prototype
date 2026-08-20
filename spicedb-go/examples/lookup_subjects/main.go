// Example lookup_subjects demonstrates finding subjects with access to a
// resource, including the wildcard/excluded-subjects case: when the server
// resolves a wildcard "*" subject, ExcludedSubjects lists the subjects
// carved out of that wildcard grant. Treating a wildcard match as "everyone
// has access" without checking ExcludedSubjects is a real over-grant risk.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"

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

	// Setup: write schema and data. `banned` carves subjects out of the
	// public/wildcard viewer grant below.
	_, err = c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user | user:*
	relation editor: user
	relation owner: user
	relation banned: user
	permission view = (viewer + editor + owner) - banned
	permission edit = editor + owner
	permission delete = owner
}`)
	if err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "editor", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	// Grant view to every user (wildcard), except those in `banned`.
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "*", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "banned", "user", "eve", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Lookup subjects
	var sawWildcard bool
	excludedIDs := map[string]bool{}
	for subject, err := range c.LookupSubjects(ctx, consistency.Full(), "document", "firstdoc", "view", "user") {
		if err != nil {
			log.Fatalf("lookup failed: %v", err)
		}

		fmt.Printf("subject:%s can view document:firstdoc (permissionship=%s)\n", subject.Subject.SubjectID, subject.Subject.Permissionship)

		if subject.Subject.SubjectID == "*" {
			sawWildcard = true
			// This is the over-grant-risk case: "*" alone would mean "every
			// user", but ExcludedSubjects carves specific subjects back out.
			// Never grant access based on the wildcard match alone.
			for _, excluded := range subject.ExcludedSubjects {
				fmt.Printf("  excluded from wildcard: user:%s\n", excluded.SubjectID)
				excludedIDs[excluded.SubjectID] = true
			}
		}
	}

	if !sawWildcard {
		log.Fatalf("expected a wildcard (*) subject in results")
	}
	if !excludedIDs["eve"] {
		log.Fatalf("expected eve to be excluded from the wildcard grant")
	}

	// bob has view via `editor`, independent of the wildcard/banned rule.
	found := map[string]bool{}
	for subject, err := range c.LookupSubjects(ctx, consistency.Full(), "document", "firstdoc", "edit", "user") {
		if err != nil {
			log.Fatalf("lookup failed: %v", err)
		}
		found[subject.Subject.SubjectID] = true
	}
	if !found["bob"] {
		log.Fatalf("expected bob in edit results")
	}

	// Clean up so later examples that write a narrower schema (e.g. one
	// without a `banned` relation) aren't blocked by leftover relationships
	// (examples run in sequence against one shared SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
