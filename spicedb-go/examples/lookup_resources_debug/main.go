// Example lookup_resources_debug demonstrates client.WithDebug(), which sets
// the new with_debug field on LookupResourcesRequest. As of this client's
// proto version, SpiceDB populates debug information only for a
// MaxDepthExceeded failure, attaching a DebugInformation to the failed
// call's error details -- there is no successful-response payload to attach
// it to, since the call errored.
//
// That payload does not get a dedicated client-native field. Root
// DESIGN.md's "RULE: Error mapping must not lose the server's detail" is
// already satisfied generically, because the gRPC status underlying every
// mapped *client.Error survives through errors.Unwrap. This example proves
// two things: that WithDebug() controls whether the server bothers
// attaching the detail at all, and the intended access path for reading it
// once attached -- status.FromError(errors.Unwrap(err)).Details(), with a
// type assertion to *v1.DebugInformation.
//
// A real SpiceDB cannot be made to hit MaxDepthExceeded on demand without
// standing up dozens of chained schema definitions, so this stands up a
// minimal stand-in that returns the failure this example exists to recover
// from -- the same way examples/error_mapping and examples/retry_policy do
// for codes the real integration SpiceDB does not produce deterministically.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// standIn always fails LookupResources with the code a real MaxDepthExceeded
// produces, attaching a DebugInformation detail ONLY when the request opted
// in via with_debug -- exactly how a real SpiceDB behaves, so a caller who
// didn't ask for debug info doesn't pay for computing it.
type standIn struct {
	v1.UnimplementedPermissionsServiceServer
}

func (s *standIn) LookupResources(req *v1.LookupResourcesRequest, _ grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	st := status.New(codes.ResourceExhausted, "max recursion depth exceeded")
	if req.GetWithDebug() {
		if withDetail, err := st.WithDetails(&v1.DebugInformation{
			SchemaUsed: "definition user {}",
		}); err == nil {
			st = withDetail
		}
	}
	return st.Err()
}

func serve() (string, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(s, &standIn{})
	go func() { _ = s.Serve(lis) }()
	return lis.Addr().String(), s.Stop
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	endpoint, stop := serve()
	defer stop()

	c, err := client.NewPlaintext(endpoint, "some-token")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	// ── 1. Without WithDebug: the failure is unchanged, but carries no ───
	//        DebugInformation detail.
	var plainErr error
	for _, e := range c.LookupResources(ctx, consistency.Full(), "document", "view", "user", "alice") {
		plainErr = e
		break
	}
	if !errors.Is(plainErr, client.ErrResourceExhausted) {
		log.Fatalf("expected ErrResourceExhausted, got: %v", plainErr)
	}
	if st, ok := status.FromError(errors.Unwrap(plainErr)); ok {
		for _, d := range st.Details() {
			if _, ok := d.(*v1.DebugInformation); ok {
				log.Fatal("did not call WithDebug(), but a DebugInformation detail came back anyway")
			}
		}
	}
	fmt.Println("without WithDebug(): ErrResourceExhausted, no debug detail attached")

	// ── 2. With WithDebug: the server attaches a DebugInformation detail ─
	var debugErr error
	for _, e := range c.LookupResources(ctx, consistency.Full(), "document", "view", "user", "alice", client.WithDebug()) {
		debugErr = e
		break
	}
	if !errors.Is(debugErr, client.ErrResourceExhausted) {
		log.Fatalf("expected ErrResourceExhausted, got: %v", debugErr)
	}
	st, ok := status.FromError(errors.Unwrap(debugErr))
	if !ok {
		log.Fatal("expected the underlying gRPC status to remain reachable through errors.Unwrap")
	}
	var info *v1.DebugInformation
	for _, d := range st.Details() {
		if i, ok := d.(*v1.DebugInformation); ok {
			info = i
		}
	}
	if info == nil {
		log.Fatal("client.WithDebug() should have caused the server to attach a DebugInformation " +
			"detail, but none was found on the mapped error's underlying status")
	}
	fmt.Printf("with WithDebug(): ErrResourceExhausted, debug detail preserved (schema_used=%q)\n",
		info.GetSchemaUsed())

	fmt.Println("lookup_resources_debug: WithDebug() controls the debug detail, reachable via the preserved gRPC status")
}
