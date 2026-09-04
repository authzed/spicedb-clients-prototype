// Example lookup_resources demonstrates finding resources a subject can
// access, including how to read the Permissionship of each result. A
// Permissionship of PermissionshipConditionalPermission means the match
// depends on caveat context that wasn't supplied — callers must not treat
// it as an unconditional grant.
//
// It also demonstrates WithLookupResourcesDebug: when a call fails because it
// exceeds SpiceDB's maximum dispatch depth, this option asks the server to
// attach a traversal trace to the failure's ReasonMetadata.
package main

import (
	"cmp"
	"context"
	"errors"
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
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "seconddoc", "editor", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
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

	// WithLookupResourcesDebug: schema built specifically to hit SpiceDB's
	// maximum dispatch depth. "node" permission view recurses through
	// "parent" one hop at a time, so a chain of parent relationships forces
	// one recursion per link. The only viewer relationship sits at the far
	// end of the chain, so resolving alice's access has to walk the whole
	// thing.
	_, err = c.WriteSchema(ctx, `definition user {}

definition node {
	relation parent: node
	relation viewer: user
	permission view = viewer + parent->view
}`)
	if err != nil {
		log.Fatalf("write schema (depth demo) failed: %v", err)
	}

	// chainLength comfortably exceeds SpiceDB's default maximum dispatch
	// depth (50), so this reliably exceeds it regardless of exactly where
	// the limit falls.
	const chainLength = 100
	var chainTxn rel.Txn
	for i := range chainLength {
		if err := chainTxn.Touch(rel.MustFromTriple(
			"node", fmt.Sprintf("n%d", i), "parent",
			"node", fmt.Sprintf("n%d", i+1), "",
		)); err != nil {
			log.Fatalf("failed to add relationship to transaction: %v", err)
		}
	}
	if err := chainTxn.Touch(rel.MustFromTriple(
		"node", fmt.Sprintf("n%d", chainLength), "viewer",
		"user", "alice", "",
	)); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if _, err := c.Write(ctx, chainTxn); err != nil {
		log.Fatalf("write relationships (depth demo) failed: %v", err)
	}

	// Without the option: the failure is a CodeFailedPrecondition *client.Error
	// whose Reason names the limit that was hit, but ReasonMetadata carries no
	// traversal detail.
	var withoutDebug error
	for _, err := range c.LookupResources(ctx, consistency.Full(), "node", "view", "user", "alice") {
		withoutDebug = err
	}
	var spicedbErr *client.Error
	if !errors.As(withoutDebug, &spicedbErr) || spicedbErr.Reason != "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED" {
		log.Fatalf("expected the deep chain to fail with ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED, got: %v", withoutDebug)
	}
	if trace, ok := spicedbErr.ReasonMetadata["dispatch_traversal_trace"]; ok {
		log.Fatalf("dispatch_traversal_trace must NOT be present without WithLookupResourcesDebug, got: %q", trace)
	}
	fmt.Printf("without WithLookupResourcesDebug: %s, no traversal trace\n", spicedbErr.Reason)

	// With the option: the same failure now carries a traversal trace in
	// ReasonMetadata, describing the dispatch path that hit the limit.
	var withDebug error
	for _, err := range c.LookupResources(ctx, consistency.Full(), "node", "view", "user", "alice", client.WithLookupResourcesDebug()) {
		withDebug = err
	}
	var spicedbDebugErr *client.Error
	if !errors.As(withDebug, &spicedbDebugErr) || spicedbDebugErr.Reason != "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED" {
		log.Fatalf("expected the deep chain to fail with ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED, got: %v", withDebug)
	}
	trace, ok := spicedbDebugErr.ReasonMetadata["dispatch_traversal_trace"]
	if !ok || trace == "" {
		log.Fatal("expected WithLookupResourcesDebug to attach a non-empty dispatch_traversal_trace")
	}
	fmt.Printf("with WithLookupResourcesDebug: %s, traversal trace: %s\n", spicedbDebugErr.Reason, trace)

	// Clean up so later examples aren't blocked by leftover relationships.
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("node")); err != nil {
		log.Fatalf("cleanup (depth demo) failed: %v", err)
	}
}
