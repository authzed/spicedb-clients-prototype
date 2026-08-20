// Example relationship_counters demonstrates registering, reading, and
// unregistering relationship counters.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

const (
	// counterTimeout bounds how long the counter may stay "still calculating"
	// before this example fails. Expiry is a failure, deliberately, and not a
	// way out of asserting.
	counterTimeout = 30 * time.Second

	counterPollInterval = 100 * time.Millisecond
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

	// Clear `document` first, so the count asserted below is an exact number
	// this example controls rather than "whatever the examples before it left
	// behind". A "non-zero" assertion passes against leftovers even when this
	// example's own writes never landed.
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if err := txn.Touch(rel.MustFromTriple("document", "seconddoc", "viewer", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	// An `editor` relationship the counter's filter must NOT count. Without a
	// relationship the filter has to exclude, a counter that ignored the
	// relation filter entirely would still report the expected number.
	if err := txn.Touch(rel.MustFromTriple("document", "firstdoc", "editor", "user", "carol", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	_, err = c.Write(ctx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	// Unregister any existing counter from a prior run (ignore errors)
	filter := rel.NewFilter("document").WithRelation("viewer")
	_ = c.UnregisterRelationshipCounter(ctx, "document_viewers")

	// Register a counter for all document viewer relationships
	err = c.RegisterRelationshipCounter(ctx, "document_viewers", filter)
	if err != nil {
		log.Fatalf("register counter failed: %v", err)
	}
	fmt.Println("registered counter: document_viewers")

	// Poll until the counter settles, instead of sleeping a fixed interval and
	// then asserting only `if !stillCalculating`. That older shape asserts
	// nothing at all on a slow run -- and nothing on ANY run if the
	// stillCalculating mapping is inverted, which is the likeliest bug on that
	// exact field. Here the timeout fails the example rather than skipping the
	// assertions.
	var result *client.CountResult
	deadline := time.Now().Add(counterTimeout)
	for {
		r, stillCalculating, err := c.CountRelationships(ctx, "document_viewers")
		if err != nil {
			log.Fatalf("count relationships failed: %v", err)
		}
		if !stillCalculating {
			if r == nil {
				log.Fatalf("counter reported settled but returned no result")
			}
			result = r
			break
		}
		fmt.Println("counter is still being calculated...")
		if time.Now().After(deadline) {
			log.Fatalf("counter document_viewers never settled within %s", counterTimeout)
		}
		time.Sleep(counterPollInterval)
	}

	fmt.Printf("document viewer count: %d (revision: %s)\n",
		result.RelationshipCount, result.Revision)

	// Exactly the two viewer relationships written above, and not the editor.
	// A count of zero -- registration silently no-op'ing, or the value never
	// being read off the response -- fails here, and so does a count of three,
	// which is what ignoring the relation filter would produce.
	if result.RelationshipCount != 2 {
		log.Fatalf("expected the counter to report exactly 2 document viewers, got %d",
			result.RelationshipCount)
	}
	if result.Revision == "" {
		log.Fatalf("expected a non-empty revision on the settled counter")
	}

	// Unregister the counter when done
	err = c.UnregisterRelationshipCounter(ctx, "document_viewers")
	if err != nil {
		log.Fatalf("unregister counter failed: %v", err)
	}
	fmt.Println("unregistered counter: document_viewers")

	// Unregistering has to actually remove it: reading a counter that is not
	// registered is an error, so a no-op unregister would leave this call
	// succeeding.
	if _, _, err := c.CountRelationships(ctx, "document_viewers"); err == nil {
		log.Fatalf("expected reading the unregistered counter to fail, but it succeeded")
	} else if !errors.Is(err, client.ErrFailedPrecondition) {
		log.Fatalf("expected client.ErrFailedPrecondition after unregistering, got: %v", err)
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
