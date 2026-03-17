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

	ctx := context.Background()

	for update, err := range c.Updates(ctx, []string{"document"}, "") {
		if err != nil {
			log.Fatalf("watch failed: %v", err)
		}
		opName := "unknown"
		switch update.Operation {
		case rel.UpdateOperationCreate:
			opName = "CREATE"
		case rel.UpdateOperationTouch:
			opName = "TOUCH"
		case rel.UpdateOperationDelete:
			opName = "DELETE"
		}
		fmt.Printf("%s: %s\n", opName, update.Relationship.String())
	}
}
