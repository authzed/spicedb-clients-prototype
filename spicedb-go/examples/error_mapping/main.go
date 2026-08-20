// Example error_mapping demonstrates the two error codes a caller actually
// recovers from -- see root DESIGN.md, "RULE: Error mapping must not lose the
// server's detail".
//
// The rule names both consequences, and this example is those two recoveries
// written out as running code:
//
//   - OUT_OF_RANGE is SpiceDB's signal that a ZedToken has expired or been
//     garbage-collected. Recovery is mechanical: discard the stale token and
//     re-read at full consistency. Collapsed into a generic error, every caller
//     would have to string-match a message to recover something the client
//     already knew the shape of.
//   - UNAUTHENTICATED is the most common error a new integration produces -- a
//     wrong, expired or rotated token. Distinguishing it is what lets a caller
//     write "refresh credentials on auth failure, page someone on internal
//     error", the one distinction that error most needs to carry.
//
// # Why this example stands up its own server
//
// Neither code is reachable from the SpiceDB the integration job starts, which
// was verified rather than assumed:
//
//   - A garbage ZedToken returns INVALID_ARGUMENT ("invalid revision
//     requested"), not OUT_OF_RANGE. A real OUT_OF_RANGE needs a revision that
//     was valid and has since been collected, and the in-memory datastore does
//     not collect it: with --datastore-gc-window=5s and 35 seconds elapsed, a
//     snapshot read at the old token still succeeded.
//   - A wrong preshared key comes back PERMISSION_DENIED from SpiceDB, not
//     UNAUTHENTICATED. That is worth knowing on its own, and part 3 below
//     asserts it against the real server so this example records what SpiceDB
//     actually does rather than what one might assume.
//
// So parts 1 and 2 drive a small SpiceDB stand-in that returns exactly these
// codes. The client's mapping is what is under test, and a stand-in is the only
// way to reach the codes deterministically.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// staleToken is the ZedToken the stand-in treats as collected.
const staleToken = "stale-zedtoken"

// standIn is a minimal SpiceDB that answers only what this example asks of it.
type standIn struct {
	v1.UnimplementedPermissionsServiceServer
}

func (s *standIn) CheckBulkPermissions(
	_ context.Context, req *v1.CheckBulkPermissionsRequest,
) (*v1.CheckBulkPermissionsResponse, error) {
	// A read pinned to a token the server no longer has: OUT_OF_RANGE, which is
	// what a caller threading revisions through a queue eventually hits.
	if at := req.GetConsistency().GetAtLeastAsFresh(); at != nil && at.GetToken() == staleToken {
		return nil, status.Error(codes.OutOfRange,
			"the specified revision has expired or been garbage collected")
	}

	// Anything else: re-read at full consistency succeeds. This is the whole
	// point of the recovery -- dropping the stale token is sufficient.
	items := make([]*v1.CheckBulkPermissionsPair, 0, len(req.GetItems()))
	for range req.GetItems() {
		items = append(items, &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
				},
			},
		})
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: items}, nil
}

// unauthenticatedServer rejects every call the way a rotated token would.
type unauthenticatedServer struct {
	v1.UnimplementedPermissionsServiceServer
}

func (s *unauthenticatedServer) CheckBulkPermissions(
	_ context.Context, _ *v1.CheckBulkPermissionsRequest,
) (*v1.CheckBulkPermissionsResponse, error) {
	return nil, status.Error(codes.Unauthenticated, "invalid token")
}

// serve starts srv on a loopback port and returns its endpoint and a stopper.
func serve(register func(*grpc.Server)) (string, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	register(s)
	go func() { _ = s.Serve(lis) }()
	return lis.Addr().String(), s.Stop
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	doc, err := rel.FromTriple("document", "readme", "view", "user", "alice", "")
	if err != nil {
		log.Fatalf("build relationship: %v", err)
	}

	// ── 1. OUT_OF_RANGE: discard the stale token, re-read at full ────────
	endpoint, stop := serve(func(s *grpc.Server) {
		v1.RegisterPermissionsServiceServer(s, &standIn{})
	})
	defer stop()

	c, err := client.NewPlaintext(endpoint, "some-token")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	_, err = c.CheckOne(ctx, consistency.AtLeast(staleToken), "view", doc)
	if !errors.Is(err, client.ErrOutOfRange) {
		log.Fatalf("a check pinned to a collected ZedToken must surface as ErrOutOfRange, "+
			"not a generic failure a caller has to string-match: got %v", err)
	}
	// The status survives the mapping (clause 2), so the server's own detail is
	// still reachable rather than reduced to a code and a rebuilt string.
	if st, ok := status.FromError(errors.Unwrap(err)); !ok || st.Code() != codes.OutOfRange {
		log.Fatalf("the underlying gRPC status must remain reachable through Unwrap: got %v", err)
	}
	fmt.Printf("stale ZedToken: ErrOutOfRange, server detail preserved (%v)\n", err)

	// The recovery the rule calls mechanical, in full: drop the token, re-read
	// at full consistency. Nothing here parses a message.
	res, err := c.CheckOne(ctx, consistency.Full(), "view", doc)
	if err != nil {
		log.Fatalf("re-reading at full consistency is the documented recovery and must succeed: %v", err)
	}
	if !res.HasPermission() {
		log.Fatal("the re-read should have returned the permission")
	}
	fmt.Println("recovery: discarded the token, re-read at full consistency, got an answer")

	// ── 2. UNAUTHENTICATED: refresh credentials, do not page anyone ──────
	authEndpoint, stopAuth := serve(func(s *grpc.Server) {
		v1.RegisterPermissionsServiceServer(s, &unauthenticatedServer{})
	})
	defer stopAuth()

	ac, err := client.NewPlaintext(authEndpoint, "rotated-token")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer func() { _ = ac.Close() }()

	_, err = ac.CheckOne(ctx, consistency.Full(), "view", doc)
	if !errors.Is(err, client.ErrUnauthenticated) {
		log.Fatalf("a rejected token must be distinguishable from an internal fault: got %v", err)
	}
	// The distinction that matters: this is NOT an internal error, so a caller
	// can branch on it. Asserting the negative is the half that would silently
	// rot if every code collapsed into one sentinel.
	if errors.Is(err, client.ErrUnavailable) {
		log.Fatal("an auth failure must not also match the unavailable sentinel")
	}
	fmt.Printf("rotated token: ErrUnauthenticated, distinct from a transport fault (%v)\n", err)

	// ── 3. What the real SpiceDB actually does with a bad preshared key ──
	//
	// PERMISSION_DENIED, not UNAUTHENTICATED. Recorded here because it is the
	// case a reader will actually hit first, and assuming otherwise is how a
	// credential-refresh branch ends up unreachable in production code.
	realEndpoint := cmp.Or(os.Getenv("SPICEDB_ENDPOINT"), "localhost:50051")
	bad, err := client.NewPlaintext(realEndpoint, "definitely-the-wrong-key")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer func() { _ = bad.Close() }()

	if _, _, err := bad.ReadSchema(ctx); !errors.Is(err, client.ErrPermissionDenied) {
		log.Fatalf("SpiceDB rejects a bad preshared key with PERMISSION_DENIED; if this now "+
			"reports something else, this example's guidance is stale and must be updated: %v", err)
	}
	fmt.Println("real SpiceDB, wrong preshared key: ErrPermissionDenied (not ErrUnauthenticated)")

	fmt.Println("error_mapping: both recoveries work without parsing a message")
}
