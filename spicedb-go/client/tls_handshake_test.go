package client_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
)

// Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server".
//
// TestNewSystemTLS in client_test.go constructs against a loopback *plaintext* port
// and asserts only that the constructor returned a non-nil client. grpc.NewClient is
// lazy, so no packet leaves the process: that test passes with an empty trust store,
// which is precisely the defect this rule exists to catch. This file is its honest
// counterpart.
//
// Gated behind SPICEDB_TLS_INTEGRATION because it needs the network. The CI step that
// sets it also greps for this test's PASS line, because `go test -run` exits 0 when
// the name filter matches nothing -- a renamed or deleted test would otherwise go
// green, reproducing this rule's own failure mode one layer up (clause 3).
const tlsHandshakeEndpoint = "grpc.authzed.com:443"

// Substrings of a failure that happened *before* any server could answer.
//
// These are matched as prose rather than by status code on purpose: gRPC reports a
// failed TLS handshake and a live server's "no healthy upstream" as the SAME code
// (codes.Unavailable), so the code cannot discriminate between "the trust store is
// empty" and "the server replied". The message can.
var trustStoreFailureSignatures = []string{
	"authentication handshake failed",
	"x509:",
	"certificate signed by unknown authority",
	"tls: ",
}

// Substrings of a failure that means the endpoint was never reached at all. Kept
// separate from the trust-store signatures so a network outage in CI reports as an
// outage rather than as a TLS regression.
var unreachableSignatures = []string{
	"no such host",
	"connection refused",
	"i/o timeout",
	"name resolver error",
}

// TestNewSystemTLS_CompletesRealHandshake drives NewSystemTLS against a real public
// endpoint and requires the TLS handshake to complete.
//
// What it asserts, and why it is not a status-code check: any gRPC status coming back
// proves the handshake completed, because producing one at all requires the far side
// to have accepted our TLS session and spoken HTTP/2 in reply. As of writing, an
// unauthenticated caller gets Unavailable "no healthy upstream" from Authzed's edge
// rather than Unauthenticated, so pinning a code here would assert a deployment detail
// of someone else's service. The distinction the rule cares about -- did we reach a
// server, or did we fail on trust material -- is what gets pinned.
func TestNewSystemTLS_CompletesRealHandshake(t *testing.T) {
	if os.Getenv("SPICEDB_TLS_INTEGRATION") == "" {
		t.Skip("set SPICEDB_TLS_INTEGRATION=1 to run the TLS handshake test (needs network)")
	}

	c, err := client.NewSystemTLS(tlsHandshakeEndpoint, "not-a-real-token")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The constructor is lazy, so it proves nothing by itself. This RPC is what forces
	// the connection, and with it the handshake -- clause 2: "where the constructor is
	// lazy, force the connection inside the test".
	_, _, err = c.ReadSchema(ctx)
	if err == nil {
		return // a successful RPC is, a fortiori, a completed handshake
	}

	msg := err.Error()
	for _, sig := range trustStoreFailureSignatures {
		require.NotContains(t, strings.ToLower(msg), sig,
			"system TLS handshake failed -- the platform trust store is probably not "+
				"loaded, or the client is supplying its own (empty) root set: %v", err)
	}
	for _, sig := range unreachableSignatures {
		require.NotContains(t, strings.ToLower(msg), sig,
			"could not reach %s at all: this is a network problem, not a TLS result, "+
				"and says nothing about the trust store: %v", tlsHandshakeEndpoint, err)
	}
}
