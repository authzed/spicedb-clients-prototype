// Example retry_policy demonstrates which calls this client retries on your
// behalf and which it deliberately does not -- see root DESIGN.md, "RULE:
// Automatic retry is for idempotent operations only".
//
// The rule exists because a silently retried mutation produces a confident
// wrong answer. If a WriteRelationships carrying OPERATION_CREATE commits and
// the response is lost, the retry comes back ALREADY_EXISTS -- and the caller
// concludes a write failed that in fact succeeded. Retrying reads is free;
// retrying mutations is only safe when the caller opted in knowing that.
//
// This example counts attempts server-side, which is the only way to tell a
// retry from its absence: from the caller's side a transparently-retried
// success and a first-try success are identical, and that is exactly the
// property that would rot unnoticed.
//
// It stands up a stand-in SpiceDB because a real one cannot be asked to fail
// transiently on demand.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// countingServer fails a configurable number of opening attempts per RPC and
// records how many attempts each RPC actually received.
type countingServer struct {
	v1.UnimplementedPermissionsServiceServer

	checkAttempts atomic.Int32
	writeAttempts atomic.Int32

	// checkFailures is how many times CheckBulkPermissions fails before
	// succeeding, and checkCode the status it fails with.
	checkFailures int32
	checkCode     codes.Code
}

func (s *countingServer) CheckBulkPermissions(
	_ context.Context, req *v1.CheckBulkPermissionsRequest,
) (*v1.CheckBulkPermissionsResponse, error) {
	if s.checkAttempts.Add(1) <= s.checkFailures {
		return nil, status.Error(s.checkCode, "transient, from the stand-in")
	}
	pairs := make([]*v1.CheckBulkPermissionsPair, 0, len(req.GetItems()))
	for range req.GetItems() {
		pairs = append(pairs, &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
				},
			},
		})
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
}

func (s *countingServer) WriteRelationships(
	_ context.Context, _ *v1.WriteRelationshipsRequest,
) (*v1.WriteRelationshipsResponse, error) {
	s.writeAttempts.Add(1)
	// Always fails, transiently. A retrying client would come back.
	return nil, status.Error(codes.Unavailable, "transient, from the stand-in")
}

func start(srv *countingServer) (*client.Client, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()

	c, err := client.NewPlaintext(lis.Addr().String(), "some-token")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	return c, func() { _ = c.Close(); s.Stop() }
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	r, err := rel.FromTriple("document", "readme", "view", "user", "alice", "")
	if err != nil {
		log.Fatalf("build relationship: %v", err)
	}

	// ── 1. A read IS retried, transparently ──────────────────────────────
	//
	// Two UNAVAILABLE responses, then success. The caller sees one successful
	// check and never learns the first two attempts happened -- which is the
	// entire value of retrying reads, and safe precisely because a repeated
	// read changes nothing.
	readSrv := &countingServer{checkFailures: 2, checkCode: codes.Unavailable}
	c, stop := start(readSrv)
	res, err := c.CheckOne(ctx, consistency.Full(), "view", r)
	if err != nil {
		log.Fatalf("a read failing transiently should have been retried to success: %v", err)
	}
	if !res.HasPermission() {
		log.Fatal("the retried check should have returned the permission")
	}
	if got := readSrv.checkAttempts.Load(); got != 3 {
		log.Fatalf("expected 2 failures plus 1 success = 3 attempts, got %d "+
			"(0 or 1 means reads are not being retried at all)", got)
	}
	fmt.Printf("read: failed twice with UNAVAILABLE, retried to success in %d attempts\n",
		readSrv.checkAttempts.Load())
	stop()

	// ── 2. A mutation is NOT retried ─────────────────────────────────────
	//
	// The same transient code, on WriteRelationships. The error reaches the
	// caller on the first attempt, so the caller -- who alone knows whether a
	// replay is safe for the transaction they built -- decides what happens
	// next. Exactly one attempt is the assertion that matters here.
	writeSrv := &countingServer{}
	wc, stopWrite := start(writeSrv)
	var txn rel.Txn
	if err := txn.Touch(r); err != nil {
		log.Fatalf("build txn: %v", err)
	}
	if _, err := wc.Write(ctx, txn); err == nil {
		log.Fatal("the stand-in always fails; the write should have surfaced an error")
	} else if !errors.Is(err, client.ErrUnavailable) {
		log.Fatalf("expected the transient failure to surface as ErrUnavailable: %v", err)
	}
	if got := writeSrv.writeAttempts.Load(); got != 1 {
		log.Fatalf("a mutation must not be retried silently: WriteRelationships saw %d attempts, "+
			"so a lost response would leave the caller believing a committed write had failed", got)
	}
	fmt.Printf("mutation: failed with UNAVAILABLE and was attempted exactly %d time -- not retried\n",
		writeSrv.writeAttempts.Load())
	stopWrite()

	// ── 3. RESOURCE_EXHAUSTED is not retryable, even on a read ───────────
	//
	// In SpiceDB this code means memory load-shed or a deterministic
	// MaxDepthExceeded. Retrying the first makes the overload worse; the second
	// can never succeed however many times it is tried. So it is deliberately
	// absent from the retryable set even though the call itself is a read.
	exhaustedSrv := &countingServer{checkFailures: 99, checkCode: codes.ResourceExhausted}
	ec, stopExhausted := start(exhaustedSrv)
	if _, err := ec.CheckOne(ctx, consistency.Full(), "view", r); err == nil {
		log.Fatal("the stand-in always fails here; the check should have surfaced an error")
	} else if !errors.Is(err, client.ErrResourceExhausted) {
		log.Fatalf("expected ErrResourceExhausted: %v", err)
	}
	if got := exhaustedSrv.checkAttempts.Load(); got != 1 {
		log.Fatalf("RESOURCE_EXHAUSTED must not be retried: saw %d attempts, which turns a "+
			"load-shedding SpiceDB into a client-driven retry storm", got)
	}
	fmt.Printf("RESOURCE_EXHAUSTED: attempted exactly %d time -- no retry storm\n",
		exhaustedSrv.checkAttempts.Load())
	stopExhausted()

	fmt.Println("retry_policy: reads retried, mutations and load-shed left to the caller")
}
