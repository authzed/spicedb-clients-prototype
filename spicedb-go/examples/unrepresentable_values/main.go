// Example unrepresentable_values demonstrates both directions of root
// DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail".
//
// The rule has two clauses that point opposite ways, and confusing them is the
// failure mode either way:
//
//  1. Data the CALLER supplied that the client cannot represent must raise a
//     typed error naming what could not be converted. The caller can see the
//     failure and fix their input, so the client neither approximates the value
//     nor drops it. Silently discarding it turns a caller's mistake into a
//     silent wrong answer.
//  2. Values the SERVER supplied that the client does not recognise must NOT
//     raise, and must map to the safe, non-permissive default -- never a grant.
//     The caller has no input to correct here, and raising would turn a routine
//     SpiceDB upgrade that adds an enum value into a client-side outage.
//
// Parts 1 and 2 below cover clause 1; part 3 covers clause 2, and needs a
// server that emits a permissionship this client has never heard of -- which is
// why it stands up a stand-in rather than using the real SpiceDB.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// futureServer answers with a permissionship value from a SpiceDB newer than
// this client -- the forward-compatibility case clause 2 governs.
type futureServer struct {
	v1.UnimplementedPermissionsServiceServer
}

func (s *futureServer) CheckBulkPermissions(
	_ context.Context, req *v1.CheckBulkPermissionsRequest,
) (*v1.CheckBulkPermissionsResponse, error) {
	pairs := make([]*v1.CheckBulkPermissionsPair, 0, len(req.GetItems()))
	for range req.GetItems() {
		pairs = append(pairs, &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					// 4242 is not a value this client's enum knows. A SpiceDB
					// that added a permissionship after this client shipped
					// would look exactly like this.
					Permissionship: v1.CheckPermissionResponse_Permissionship(4242),
				},
			},
		})
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
}

func main() {
	// ── 1. Caller data: caveat context that will not convert ─────────────
	//
	// A Go channel has no protobuf representation. The rule's answer is to
	// fail, loudly, naming the offending key -- not to drop it, which would
	// leave a caveat evaluating against context the caller believes it sent.
	r, err := rel.FromTriple("document", "readme", "viewer", "user", "alice", "")
	if err != nil {
		log.Fatalf("build relationship: %v", err)
	}
	withBadContext := r.WithCaveat("only_on_tuesday", map[string]any{
		"day":      "tuesday",
		"impostor": make(chan int),
	})

	var txn rel.Txn
	err = txn.Touch(withBadContext)
	if err == nil {
		log.Fatal("caveat context holding an unconvertible value must not be accepted silently")
	}
	if !errors.Is(err, rel.ErrInvalidCaveatContext) {
		log.Fatalf("the failure must be typed, not a bare string: got %v", err)
	}
	// Naming the key is what makes the error actionable: a caller with a large
	// context map should not have to bisect it to find the bad entry.
	if !strings.Contains(err.Error(), "impostor") {
		log.Fatalf("the error must name the key that could not be converted, got: %v", err)
	}
	fmt.Printf("unconvertible caveat context: refused, naming the key (%v)\n", err)

	// ── 2. Caller data: a filter the wire format cannot express ──────────
	//
	// A subject ID with no subject type is not a narrower filter -- the wire
	// format simply drops it, so the filter silently WIDENS. Applied to
	// DeleteRelationships that is the difference between deleting alice's
	// relationships and deleting every relationship on every document.
	_, err = rel.NewFilter("document").WithSubjectID("alice").ToProto()
	if err == nil {
		log.Fatal("a filter whose subject constraint the wire cannot express must fail, not widen")
	}
	if !errors.Is(err, rel.ErrInvalidFilter) {
		log.Fatalf("the failure must be typed: got %v", err)
	}
	fmt.Printf("subject ID without subject type: refused rather than silently widened (%v)\n", err)

	// The same filter with the missing piece supplied converts fine, which is
	// what makes the check above a real constraint rather than a blanket ban.
	if _, err := rel.NewFilter("document").WithSubjectType("user").WithSubjectID("alice").ToProto(); err != nil {
		log.Fatalf("a fully-specified subject filter must convert: %v", err)
	}
	fmt.Println("...and converts once SubjectType is supplied")

	// ── 3. Server data: an enum this client has never seen ───────────────
	//
	// The opposite posture. This must not raise, and must not be a grant.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(srv, &futureServer{})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	c, err := client.NewPlaintext(lis.Addr().String(), "some-token")
	if err != nil {
		log.Fatalf("create client: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	check, err := rel.FromTriple("document", "readme", "view", "user", "alice", "")
	if err != nil {
		log.Fatalf("build relationship: %v", err)
	}
	res, err := c.CheckOne(ctx, consistency.Full(), "view", check)
	if err != nil {
		log.Fatalf("an unrecognised server enum must not raise -- that would turn a SpiceDB "+
			"upgrade into a client-side outage: %v", err)
	}
	if res.HasPermission() {
		log.Fatal("SECURITY: an unrecognised permissionship was treated as a grant")
	}
	fmt.Println("unknown server permissionship: no error, and not a grant")

	fmt.Println("unrepresentable_values: caller data fails loudly, server data degrades safely")
}
