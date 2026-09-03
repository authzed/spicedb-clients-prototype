// Example lookup_resources demonstrates finding resources a subject can
// access, including how to read the Permissionship of each result. A
// Permissionship of PermissionshipConditionalPermission means the match
// depends on caveat context that wasn't supplied — callers must not treat
// it as an unconditional grant.
package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// debugCapturingServer is a stand-in PermissionsService that records whether
// the most recent LookupResources request carried WithDebug, so
// WithLookupResourcesDebug can be proven to reach the wire without needing to
// construct a real maximum-recursion-depth failure against a live SpiceDB.
type debugCapturingServer struct {
	v1.UnimplementedPermissionsServiceServer

	gotWithDebug bool
}

func (s *debugCapturingServer) LookupResources(req *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	s.gotWithDebug = req.GetWithDebug()
	return stream.Send(&v1.LookupResourcesResponse{
		ResourceObjectId: "doc1",
		Permissionship:   v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
	})
}

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

	// WithLookupResourcesDebug: requests that a LookupResources failure caused
	// by exceeding the maximum recursion depth carry additional debug context
	// in the error's details (surfaced via Error.Reason/Error.ReasonMetadata,
	// same as any other server ErrorInfo -- see client/errors.go). Provoking a
	// real depth-exceeded failure needs a deeply recursive schema this example
	// doesn't otherwise need, so this proves the option reaches the wire
	// against a stand-in PermissionsService instead.
	debugSrv := &debugCapturingServer{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, debugSrv)
	go func() { _ = grpcServer.Serve(lis) }()
	defer grpcServer.Stop()

	debugClient, err := client.NewPlaintext(lis.Addr().String(), "some-token")
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}
	defer func() { _ = debugClient.Close() }()

	for _, err := range debugClient.LookupResources(ctx, consistency.MinLatency(), "document", "view", "user", "alice") {
		if err != nil {
			log.Fatalf("lookup against stand-in failed: %v", err)
		}
	}
	if debugSrv.gotWithDebug {
		log.Fatal("WithDebug should be false when WithLookupResourcesDebug is not passed")
	}

	for _, err := range debugClient.LookupResources(ctx, consistency.MinLatency(), "document", "view", "user", "alice", client.WithLookupResourcesDebug()) {
		if err != nil {
			log.Fatalf("lookup against stand-in failed: %v", err)
		}
	}
	if !debugSrv.gotWithDebug {
		log.Fatal("WithLookupResourcesDebug should have set WithDebug on the wire request")
	}
	fmt.Println("WithLookupResourcesDebug: confirmed WithDebug reaches the wire request")

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	if _, err := c.DeleteRelationships(ctx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
