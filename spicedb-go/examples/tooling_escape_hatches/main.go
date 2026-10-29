// Example tooling_escape_hatches demonstrates what CLI and server tooling
// (such as zed) needs beyond ordinary checks: a client over a caller-owned
// connection, the caller's own retry and deadline policy, resumable paging,
// response metadata, and protocol-shaped output.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/client/protoconv"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// bearer attaches the preshared key; a caller-owned connection carries its own credentials.
type bearer string

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(b)}, nil
}
func (bearer) RequireTransportSecurity() bool { return false }

func main() {
	ctx := context.Background()
	endpoint := cmp.Or(os.Getenv("SPICEDB_ENDPOINT"), "localhost:50051")
	token := cmp.Or(os.Getenv("SPICEDB_TOKEN"), "somerandomkeyhere")

	// 1. A connection the caller owns: in-memory, buffered listener, or (here) dialed by hand.
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(bearer(token)), grpc.WithDisableRetry())
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// 2. The caller applies its own retry policy and has no client deadline.
	c, err := client.NewFromConn(conn, client.WithRetries(client.RetriesDisabled), client.WithDefaultDeadline(client.DefaultDeadlineDisabled))
	if err != nil {
		log.Fatalf("NewFromConn: %v", err)
	}
	if _, err := client.NewFromConn(conn, client.WithInsecure()); !errors.Is(err, client.ErrInvalidArgument) {
		log.Fatalf("transport options must be rejected on a supplied connection, got %v", err)
	}

	// 3. Response metadata, the escape hatch for transport-level data.
	var header metadata.MD
	schema, err := c.ReadSchema(ctx, client.ReadSchemaRequest{}, client.WithResponseMetadata[client.ReadSchemaOption](&header, nil))
	if client.CodeOf(err) == client.CodeNotFound {
		_, err = c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: "definition user {}\n\ndefinition document {\n\trelation viewer: user\n}"})
		if err == nil {
			schema, err = c.ReadSchema(ctx, client.ReadSchemaRequest{}, client.WithResponseMetadata[client.ReadSchemaOption](&header, nil))
		}
	}
	if err != nil {
		log.Fatalf("read schema: %v", err)
	}
	if len(header.Get("content-type")) == 0 {
		log.Fatalf("response headers were not captured")
	}
	if !strings.Contains(schema.Schema, "definition document") {
		log.Fatalf("expected a document definition, got %q", schema.Schema)
	}
	fmt.Printf("captured %d response headers\n", len(header))

	// 4. Resumable paging: stop after one item, then resume from its cursor.
	// SpiceDB accepts a cursor only for an identical call, so the resumed read
	// repeats the request and page size.
	var txn rel.Txn
	for _, who := range []string{"ann", "ben", "cat"} {
		if err := txn.Touch(rel.MustFromTuple("document:tooling#viewer@user:" + who)); err != nil {
			log.Fatalf("touch: %v", err)
		}
	}
	written, err := c.Write(ctx, client.WriteRequest{Transaction: txn})
	if err != nil {
		log.Fatalf("write: %v", err)
	}
	req := client.ReadRelationshipsRequest{Filter: rel.MustNewFilter("document").WithResourceID("tooling"), Consistency: consistency.AtLeast(written.Revision)}
	var first client.ReadRelationship
	for r, err := range c.ReadRelationships(ctx, req, client.WithReadRelationshipsPageSize(1)) {
		if err != nil {
			log.Fatalf("read: %v", err)
		}
		first = r
		break
	}
	seen := []string{first.SubjectID()}
	for r, err := range c.ReadRelationships(ctx, req, client.WithReadRelationshipsPageSize(1), client.WithReadRelationshipsCursor(first.AfterResultCursor)) {
		if err != nil {
			log.Fatalf("resumed read: %v", err)
		}
		seen = append(seen, r.SubjectID())
	}
	if len(seen) != 3 || first.ReadAt.String() == "" {
		log.Fatalf("resume should cover every relationship exactly once, got %v (read at %q)", seen, first.ReadAt)
	}
	fmt.Printf("read %s, then resumed: %v\n", first.Relationship, seen[1:])

	// 5. Protocol-shaped output for stable JSON.
	result, err := c.Check(ctx, client.PermissionCheck{
		Resource: rel.MustNewObject("document", "tooling"), Permission: "viewer",
		Subject: rel.MustNewSubject(rel.MustNewObject("user", "ann"), ""), Consistency: consistency.AtLeast(written.Revision),
	})
	if err != nil {
		log.Fatalf("check: %v", err)
	}
	p, err := protoconv.CheckResultToProto(result)
	if err != nil {
		log.Fatalf("protoconv: %v", err)
	}
	js, err := protojson.Marshal(p)
	if err != nil || !strings.Contains(string(js), "PERMISSIONSHIP_HAS_PERMISSION") {
		log.Fatalf("expected a granted check in JSON, got %s (%v)", js, err)
	}
	fmt.Printf("check as JSON: %s\n", js)

	// 6. Close never closes a caller-owned connection.
	if err := c.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}
	other, _ := client.NewFromConn(conn)
	if _, err := other.ReadSchema(ctx, client.ReadSchemaRequest{}); err != nil {
		log.Fatalf("connection should outlive the client: %v", err)
	}
	fmt.Println("caller-owned connection still open after Close")
}
