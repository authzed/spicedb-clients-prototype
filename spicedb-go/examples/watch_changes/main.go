// Example watch_changes demonstrates watching for relationship changes with a
// bounded consumer that cancels the stream explicitly when it is done.
//
// Watch is an open-ended server stream: it never completes on its own. A
// consumer that only `break`s out of the range loop is relying on the
// iterator's cleanup to release the gRPC stream, and nothing here would notice
// if that cleanup stopped happening. So this example does the opposite of a
// print-and-hope loop:
//
//  1. it subscribes from a known revision,
//  2. makes a write that must produce a specific update,
//  3. consumes until it has observed exactly that update,
//  4. cancels the stream explicitly, and
//  5. requires the consumer to come back promptly afterwards.
//
// Step 5 is the assertion that carries root DESIGN.md, "RULE: Abandoning a
// stream must release it": if the caller's context were not threaded into the
// Watch RPC, Recv would stay parked on a stream with no further events and this
// example would fail on releaseTimeout instead of quietly passing.
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
	// updateTimeout bounds the wait for the update this example wrote to come
	// back out of the stream. Generous for a local SpiceDB -- the point is that
	// the example fails, with a message, instead of hanging forever.
	updateTimeout = 30 * time.Second

	// releaseTimeout bounds how long the consumer may take to return after the
	// stream is cancelled. A released stream unblocks Recv immediately; a
	// leaked one never wakes, because a quiet watch stream has nothing else to
	// deliver.
	releaseTimeout = 10 * time.Second
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

	// The same schema the other examples write, so this example does not
	// narrow it out from under them (they share one SpiceDB).
	if _, err := c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`); err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	// Clear `document` before writing anything. A TOUCH of a relationship that
	// already exists, unchanged, is not a change: SpiceDB emits no watch event
	// for it. Leaving a previous run's data in place would therefore make this
	// example wait for an update the server has no reason to send.
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	// A seed write establishes the revision to watch from, so the stream
	// cannot replay whatever earlier examples left behind and cannot miss the
	// write made below.
	var seed rel.Txn
	if err := seed.Touch(rel.MustFromTriple("document", "watched", "viewer", "user", "seed", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	startRevision, err := c.Write(ctx, seed)
	if err != nil {
		log.Fatalf("seed write failed: %v", err)
	}

	// The consumer runs in its own goroutine so this example can make the
	// write that produces the update, and then cancel the stream and require
	// the consumer to come back. cancelWatch is the explicit cancellation:
	// calling it is what releases the stream.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	observed := make(chan rel.Update, 1)
	checkpointSeen := make(chan string, 1)
	consumerDone := make(chan error, 1)

	go func() {
		var iterErr error

		// client.WithIncludeCheckpoints() asks the server for periodic
		// checkpoint events in addition to relationship updates --
		// recommended if this SpiceDB instance is running behind a proxy that
		// aborts idle connections, since a checkpoint keeps the stream alive
		// even when there are no changes.
		for event, err := range c.Updates(watchCtx, []string{"document"}, startRevision, client.WithIncludeCheckpoints()) {
			if err != nil {
				iterErr = err
				break
			}

			// event.ChangesThrough is a resume point: keep it and pass it as
			// startRevision on a later Updates call to pick back up after a
			// dropped stream, instead of reprocessing everything since the
			// original startRevision or silently losing changes by restarting
			// from head.
			if event.IsCheckpoint {
				// A checkpoint carries no updates -- it exists to advertise a
				// fresh resume point and keep the stream alive.
				if len(event.Updates) != 0 {
					iterErr = fmt.Errorf("checkpoint at %s carried %d updates; a checkpoint carries none",
						event.ChangesThrough, len(event.Updates))
					break
				}
				select {
				case checkpointSeen <- event.ChangesThrough:
				default:
				}
				fmt.Printf("CHECKPOINT: revision=%s\n", event.ChangesThrough)
				continue
			}

			for _, update := range event.Updates {
				var opName string
				switch update.Operation {
				case rel.UpdateOperationCreate:
					opName = "CREATE"
				case rel.UpdateOperationTouch:
					opName = "TOUCH"
				case rel.UpdateOperationDelete:
					opName = "DELETE"
				case rel.UpdateOperationUnspecified:
					// The server sent an operation this client doesn't recognize. A
					// real consumer must NOT treat this as a write -- re-read the
					// relationship, or fail the mirror closed.
					opName = "UNSPECIFIED (unrecognized by this client)"
				}
				fmt.Printf("%s: %s (through %s)\n", opName, update.Relationship.String(), event.ChangesThrough)

				if update.Relationship.ResourceType == "document" &&
					update.Relationship.ResourceID == "watched" &&
					update.Relationship.ResourceRelation == "editor" &&
					update.Relationship.SubjectID == "bob" {
					select {
					case observed <- update:
					default:
					}
				}
			}
		}

		consumerDone <- iterErr
	}()

	// The write the consumer above is waiting for.
	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "watched", "editor", "user", "bob", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	if _, err := c.Write(ctx, txn); err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	var update rel.Update
	select {
	case update = <-observed:
	case err := <-consumerDone:
		log.Fatalf("watch stream ended before the expected update arrived: %v", err)
	case <-time.After(updateTimeout):
		log.Fatalf("did not observe document:watched#editor@user:bob within %s", updateTimeout)
	}

	// The update must be the one that was written, not merely "an update".
	if update.Operation != rel.UpdateOperationTouch && update.Operation != rel.UpdateOperationCreate {
		log.Fatalf("expected a CREATE or TOUCH for the relationship just written, got %v", update.Operation)
	}
	if got := update.Relationship.SubjectType; got != "user" {
		log.Fatalf("expected subject type \"user\", got %q", got)
	}
	fmt.Printf("observed the expected update: %s\n", update.Relationship.String())

	// A checkpoint must arrive too, or WithIncludeCheckpoints never reached
	// the server and the checkpoint branch above is decoration.
	select {
	case revision := <-checkpointSeen:
		fmt.Printf("observed a checkpoint through revision %s\n", revision)
	case err := <-consumerDone:
		log.Fatalf("watch stream ended before a checkpoint arrived: %v", err)
	case <-time.After(updateTimeout):
		log.Fatalf("no checkpoint event within %s -- WithIncludeCheckpoints did not reach the server",
			updateTimeout)
	}

	// Abandon the stream explicitly. Cancelling before waiting is the whole
	// point: a `break` alone leaves the release to the iterator's cleanup, and
	// this example wants a failure -- not a silent leak -- if that stops
	// working.
	cancelWatch()

	select {
	case err := <-consumerDone:
		// grpc-go reports a client-cancelled stream as codes.Canceled, which
		// this client maps to its own sentinel. Anything else means the stream
		// ended for some reason other than the cancellation above.
		if !errors.Is(err, client.ErrCanceled) {
			log.Fatalf("expected the cancelled watch to end with client.ErrCanceled, got: %v", err)
		}
		fmt.Printf("watch stream released after cancellation: %v\n", err)
	case <-time.After(releaseTimeout):
		log.Fatalf("watch consumer did not return %s after the stream was cancelled: "+
			"cancelling the caller's context did not release the stream", releaseTimeout)
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
