// Example raw_escape_hatch demonstrates reaching past the idiomatic API with
// RawProto.
//
// RawProto accesses the existing authenticated generated clients. This example
// writes transaction metadata, reads it from a raw watch response, and compares
// native and raw singular permission checks. The raw path creates no connection
// and transfers no ownership. It bypasses native validation, errors, retry and
// total budgets, so callers supply context deadlines and cancel streams directly.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/authzed/spicedb-clients/spicedb-go/examples/internal/exampleutil"

	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
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

	if _, err := c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: `definition user {}

definition document {
	relation viewer: user
	permission view = viewer
}`}); err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	// Clear first: a TOUCH of an already-identical relationship is not a
	// change, so SpiceDB would emit no watch event for it and the read-back
	// below would wait forever on a rerun.
	if _, err := c.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: exampleutil.Must(rel.NewFilter("document"))}); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	// A seed write fixes the revision the watch below starts from, so it sees
	// the metadata write and nothing that came before it.
	var seed rel.Txn
	if err := seed.Touch(exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "ledger"), "viewer", exampleutil.Subject(exampleutil.Object("user", "seed"), "")))); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	seedRevision, err := c.Write(ctx, client.WriteRequest{Transaction: seed})
	if err != nil {
		log.Fatalf("seed write failed: %v", err)
	}

	// ── 1. A proto field the idiomatic API does not expose ───────────────
	//
	// The bearer token rides this client's own connection credentials, so
	// there is nothing extra to attach.
	metadata, err := structpb.NewStruct(map[string]any{
		"correlation_id": "example-42",
		"actor":          "billing-job",
	})
	if err != nil {
		log.Fatalf("build transaction metadata failed: %v", err)
	}

	// Sending the metadata proves nothing on its own: a client that dropped
	// the field would look identical from here, because WriteRelationships
	// does not echo it back. The only place it becomes observable is the Watch
	// stream, so the read-back below is what makes this example able to fail.
	// Watch is started before the write so the event cannot be missed.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	watchStream, err := c.RawProto().WatchServiceClient.Watch(watchCtx, &v1.WatchRequest{
		OptionalObjectTypes: []string{"document"},
		OptionalStartCursor: &v1.ZedToken{Token: seedRevision.Revision.String()},
	})
	if err != nil {
		log.Fatalf("raw watch failed: %v", err)
	}

	type watched struct {
		metadata map[string]any
		err      error
	}
	seen := make(chan watched, 1)
	go func() {
		for {
			resp, err := watchStream.Recv()
			if err != nil {
				seen <- watched{err: err}
				return
			}
			if md := resp.GetOptionalTransactionMetadata(); md != nil {
				seen <- watched{metadata: md.AsMap()}
				return
			}
		}
	}()

	written, err := c.RawProto().PermissionsServiceClient.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{
		Updates: []*v1.RelationshipUpdate{{
			Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
			Relationship: &v1.Relationship{
				Resource: &v1.ObjectReference{ObjectType: "document", ObjectId: "ledger"},
				Relation: "viewer",
				Subject: &v1.SubjectReference{
					Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "jimmy"},
				},
			},
		}},
		OptionalTransactionMetadata: metadata,
	})
	if err != nil {
		log.Fatalf("raw write failed: %v", err)
	}
	revision := written.GetWrittenAt().GetToken()
	fmt.Printf("raw write committed at revision %s\n", revision)

	// Read the metadata back out of the Watch stream.
	select {
	case got := <-seen:
		if got.err != nil {
			log.Fatalf("raw watch stream failed before the metadata arrived: %v", got.err)
		}
		fmt.Printf("watch reported transaction metadata: %v\n", got.metadata)
		if got.metadata["correlation_id"] != "example-42" {
			log.Fatalf("expected correlation_id \"example-42\" on the watched transaction, got %v",
				got.metadata["correlation_id"])
		}
		if got.metadata["actor"] != "billing-job" {
			log.Fatalf("expected actor \"billing-job\" on the watched transaction, got %v",
				got.metadata["actor"])
		}
	case <-time.After(30 * time.Second):
		log.Fatalf("no watch event carried OptionalTransactionMetadata within 30s: " +
			"the metadata sent on the raw write never reached the server")
	}

	// Abandoning the raw stream is the caller's job on this path: RawProto
	// hands back the generated client, so there is no iterator cleanup to lean
	// on. See root DESIGN.md, "RULE: Abandoning a stream must release it".
	cancelWatch()

	// The idiomatic API picks up right where the raw call left off -- same
	// client, same connection, including read-your-writes on the raw revision.
	r := exampleutil.Must(rel.NewRelationship(exampleutil.Object("document", "ledger"), "view", exampleutil.Subject(exampleutil.Object("user", "jimmy"), "")))
	result, err := c.Check(ctx, client.PermissionCheck{Resource: (r).Resource(), Subject: (r).Subject(), Permission: "view", Consistency: consistency.AtLeast(consistency.NewZedToken(revision))})
	if err != nil {
		log.Fatalf("idiomatic check failed: %v", err)
	}
	fmt.Printf("user:jimmy can view document:ledger: %v\n", result.HasPermission())
	if !result.HasPermission() {
		log.Fatal("expected jimmy to have view permission")
	}

	// Compare the same singular check through the raw generated client.
	//
	// A raw call gets no deadline of the library's -- set one on the context.
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	single, err := c.RawProto().PermissionsServiceClient.CheckPermission(callCtx, &v1.CheckPermissionRequest{
		Consistency: &v1.Consistency{
			Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true},
		},
		Resource:   &v1.ObjectReference{ObjectType: "document", ObjectId: "ledger"},
		Permission: "view",
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "jimmy"},
		},
	})
	if err != nil {
		log.Fatalf("raw CheckPermission failed: %v", err)
	}
	fmt.Printf("raw CheckPermission permissionship: %v\n", single.GetPermissionship())
	if single.GetPermissionship() != v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION {
		log.Fatal("expected PERMISSIONSHIP_HAS_PERMISSION")
	}

	// Clean up so a later example isn't blocked by leftover relationships.
	if _, err := c.DeleteAllRelationshipsNonAtomic(ctx, client.DeleteAllRequest{Filter: exampleutil.Must(rel.NewFilter("document")).WithResourceID("ledger")}); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}

	// Close the CLIENT, never the object RawProto returned -- it holds this
	// client's connection, and (*client.Client).Close is what releases it.
	fmt.Println("done")
}
