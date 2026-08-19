// Example raw_escape_hatch demonstrates reaching past the idiomatic API with
// RawProto.
//
// Every wrapper eventually meets a request the wrapper does not express. This
// client's answer is RawProto: the underlying proto client, with the four
// generated service clients it makes its own calls through -- a workaround
// short of forking the library. Root DESIGN.md, "What NOT To Do", allows
// exactly this as "clearly marked secondary API".
//
// The gaps demonstrated here are real, not hypothetical:
//
//  1. WriteRelationshipsRequest.OptionalTransactionMetadata is a proto field
//     this client does not surface anywhere. Applications use it to stamp an
//     audit correlation ID onto a write, which comes back out of the Watch
//     stream.
//  2. CheckPermission -- the single-check RPC. The idiomatic CheckOne routes
//     every check through CheckBulkPermissions, so the raw client is how you
//     drive the unary RPC itself.
//
// Note what this example does NOT do: build a second proto client. RawProto
// returns THIS client's connection, configured exactly as it was configured
// (including anything passed to WithDialOptions) and carrying the same
// credentials, so the raw path cannot end up on a different transport than the
// idiomatic one.
//
// What you give up on the raw path, and why the idiomatic methods stay the
// default: no *client.Error mapping (you handle grpc status codes yourself), no
// retry on a transient failure, and no deadline of the library's -- set one on
// the context.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

func main() {
	c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()

	if _, err := c.WriteSchema(ctx, `definition user {}

definition document {
	relation viewer: user
	permission view = viewer
}`); err != nil {
		log.Fatalf("write schema failed: %v", err)
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

	// The idiomatic API picks up right where the raw call left off -- same
	// client, same connection, including read-your-writes on the raw revision.
	r := rel.MustFromTriple("document", "ledger", "view", "user", "jimmy", "")
	result, err := c.CheckOne(ctx, consistency.AtLeast(revision), "view", r)
	if err != nil {
		log.Fatalf("idiomatic check failed: %v", err)
	}
	fmt.Printf("user:jimmy can view document:ledger: %v\n", result.HasPermission())
	if !result.HasPermission() {
		log.Fatal("expected jimmy to have view permission")
	}

	// ── 2. An RPC the idiomatic API routes around ────────────────────────
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
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document").WithResourceID("ledger")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}

	// Close the CLIENT, never the object RawProto returned -- it holds this
	// client's connection, and (*client.Client).Close is what releases it.
	fmt.Println("done")
}
