// Example call_deadlines demonstrates bounding calls with a context deadline
// -- see root DESIGN.md, "RULE: A unary call must have a deadline".
//
// Unlike the other six clients, this one has no timeout parameter of its own:
// the caller's context.Context is the bound, and grpc-go's retry policy reuses
// that same context across every attempt, so a context deadline bounds the
// whole operation (attempts, backoff and pagination) rather than each attempt.
// That makes `ctx` the deadline API, and passing a bare context.Background() to
// a SpiceDB call the thing this rule exists to prevent.
//
// The failure this guards against is a *wedged* server: one that accepts the
// connection and then never answers. Nothing looks wrong at the transport
// level, so an unbounded call hangs forever rather than erroring. An example
// that only showed a fast local call succeeding would pass identically whether
// or not the deadline ever reached the wire, so this one stands up a socket
// that behaves exactly that way and requires the call to come back
// DeadlineExceeded.
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

	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// wedgedCallBudget is the deadline given to the calls against the wedged
// server below. Short, because the point is to watch it expire.
const wedgedCallBudget = 2 * time.Second

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

	// ── 1. The ordinary case: bound every unary call ─────────────────────
	//
	// 30 seconds is generous for a local SpiceDB. What matters is that the
	// budget exists at all, and that it is derived once and passed down --
	// not that it is small.
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSetup()

	if _, err := c.WriteSchema(setupCtx, `definition user {}

definition document {
	relation viewer: user
	relation editor: user
	relation owner: user
	permission view = viewer + editor + owner
	permission edit = editor + owner
	permission delete = owner
}`); err != nil {
		log.Fatalf("write schema failed: %v", err)
	}

	if _, err := c.DeleteRelationships(setupCtx, rel.NewFilter("document")); err != nil {
		log.Fatalf("clearing document relationships failed: %v", err)
	}

	var txn rel.Txn
	if err := txn.Touch(rel.MustFromTriple("document", "readme", "viewer", "user", "alice", "")); err != nil {
		log.Fatalf("failed to add relationship to transaction: %v", err)
	}
	revision, err := c.Write(setupCtx, txn)
	if err != nil {
		log.Fatalf("write relationships failed: %v", err)
	}

	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCheck()

	result, err := c.CheckOne(checkCtx, consistency.AtLeast(revision), "view",
		rel.MustFromTriple("document", "readme", "view", "user", "alice", ""))
	if err != nil {
		log.Fatalf("check failed: %v", err)
	}
	fmt.Printf("user:alice can view document:readme (5s budget): %v\n", result.HasPermission())
	if !result.HasPermission() {
		log.Fatal("expected alice to have view permission")
	}

	// ── 2. The case the rule is about: a server that never answers ───────
	//
	// This listener accepts TCP connections at the kernel level and never
	// speaks gRPC: the socket is never handed to a reader, so the HTTP/2
	// server preface never arrives. That is what a wedged SpiceDB looks like
	// from a client -- an open, healthy-looking connection with no reply
	// behind it -- and it is why "the connection worked" is not a bound.
	wedged, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("failed to open the wedged listener: %v", err)
	}
	defer func() { _ = wedged.Close() }()

	wedgedClient, err := client.NewPlaintext(wedged.Addr().String(), "somerandomkeyhere")
	if err != nil {
		log.Fatalf("failed to create the wedged client: %v", err)
	}
	defer func() { _ = wedgedClient.Close() }()

	wedgedCtx, cancelWedged := context.WithTimeout(context.Background(), wedgedCallBudget)
	defer cancelWedged()

	// The call runs on its own goroutine behind a watchdog. If the deadline
	// never reaches the wire -- a client that dropped the caller's context on
	// the way down, say -- this call does not return at all, and an example
	// that simply waited for it would hang the CI job rather than fail it.
	type callResult struct {
		err     error
		elapsed time.Duration
	}
	wedgedResult := make(chan callResult, 1)
	go func() {
		started := time.Now()
		_, err := wedgedClient.CheckOne(wedgedCtx, consistency.Full(), "view",
			rel.MustFromTriple("document", "readme", "view", "user", "alice", ""))
		wedgedResult <- callResult{err: err, elapsed: time.Since(started)}
	}()

	var elapsed time.Duration
	select {
	case got := <-wedgedResult:
		err, elapsed = got.err, got.elapsed
	case <-time.After(wedgedCallBudget + 15*time.Second):
		log.Fatalf("a call with a %s deadline had not returned after %s against a server that "+
			"never answers: the caller's deadline is not reaching the RPC",
			wedgedCallBudget, wedgedCallBudget+15*time.Second)
	}

	if err == nil {
		log.Fatal("expected the call against a server that never answers to fail")
	}
	fmt.Printf("wedged server: %v after %s\n", err, elapsed.Round(time.Millisecond))

	// The specific error matters. "An error occurred" would also be satisfied
	// by Unavailable from a refused connection -- which is what this assertion
	// would degrade into if the listener above stopped accepting, and which
	// says nothing at all about deadlines.
	if !errors.Is(err, client.ErrDeadlineExceeded) {
		log.Fatalf("expected client.ErrDeadlineExceeded from the wedged server, got: %v", err)
	}
	var spiceErr *client.Error
	if !errors.As(err, &spiceErr) {
		log.Fatalf("expected a native *client.Error, got: %v", err)
	}
	if spiceErr.Code != client.CodeDeadlineExceeded {
		log.Fatalf("expected client.CodeDeadlineExceeded, got %v", spiceErr.Code)
	}

	// And it has to expire on the caller's schedule, not eventually. Without a
	// deadline this call does not return at all.
	if elapsed > wedgedCallBudget+5*time.Second {
		log.Fatalf("the %s deadline took %s to fire: the caller's context is not bounding the call",
			wedgedCallBudget, elapsed)
	}

	// A context whose deadline has already passed fails immediately: the same
	// budget covers retries and pages, so there is nothing left to spend.
	expired, cancelExpired := context.WithTimeout(context.Background(), 0)
	defer cancelExpired()
	if _, err := wedgedClient.DeleteRelationships(expired, rel.NewFilter("document")); !errors.Is(err, client.ErrDeadlineExceeded) {
		log.Fatalf("expected an already-expired context to fail the call with "+
			"client.ErrDeadlineExceeded, got: %v", err)
	}

	// ── 3. Streaming calls are not bounded by a unary budget ─────────────
	//
	// ImportRelationships is client-streaming: its duration scales with the
	// size of the caller's dataset, not with server latency, so no fixed
	// deadline of the library's is applied to it. The caller may still impose
	// one -- that is what the context is for -- and does here.
	importCtx, cancelImport := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelImport()

	numLoaded, err := c.ImportRelationships(importCtx, func(yield func(rel.Relationship) bool) {
		for i := range 50 {
			r := rel.MustFromTriple("document", fmt.Sprintf("bulk-%d", i), "viewer", "user", "alice", "")
			if !yield(r) {
				return
			}
		}
	})
	if err != nil {
		log.Fatalf("bulk import failed: %v", err)
	}
	fmt.Printf("imported %d relationships under an explicit 60s budget\n", numLoaded)
	if numLoaded != 50 {
		log.Fatalf("expected 50 relationships imported, got %d", numLoaded)
	}

	// Clean up so later examples that write a narrower schema aren't blocked
	// by leftover relationships (examples run in sequence against one shared
	// SpiceDB instance).
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	if _, err := c.DeleteRelationships(cleanupCtx, rel.NewFilter("document")); err != nil {
		log.Fatalf("cleanup failed: %v", err)
	}
}
