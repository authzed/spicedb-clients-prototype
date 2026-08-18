// Example watch_changes demonstrates watching for relationship changes.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	// client.WithIncludeCheckpoints() asks the server for periodic
	// checkpoint events in addition to relationship updates --
	// recommended if this SpiceDB instance is running behind a proxy that
	// aborts idle connections, since a checkpoint keeps the stream alive
	// even when there are no changes.
	var resumeToken string
	for event, err := range c.Updates(ctx, []string{"document"}, "", client.WithIncludeCheckpoints()) {
		if err != nil {
			log.Fatalf("watch failed: %v", err)
		}

		// event.ChangesThrough is a resume point: keep it and pass it as
		// startRevision on a later Updates call to pick back up after a
		// dropped stream, instead of reprocessing everything since the
		// original startRevision or silently losing changes by restarting
		// from head.
		resumeToken = event.ChangesThrough

		if event.IsCheckpoint {
			// A checkpoint carries no updates -- it exists to advertise a
			// fresh resume point and keep the stream alive.
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
			fmt.Printf("%s: %s\n", opName, update.Relationship.String())
		}
	}
	fmt.Printf("last resume token seen: %s\n", resumeToken)
}
