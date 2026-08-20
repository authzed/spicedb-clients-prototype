package client_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
)

func TestNewPlaintext(t *testing.T) {
	c, err := client.NewPlaintext("passthrough:///localhost:50051", "test-token")
	require.NoError(t, err)
	require.NotNil(t, c)
}

// TestNewSystemTLS covers construction only, and deliberately says so: the target is a
// loopback *plaintext* port and grpc.NewClient is lazy, so no packet leaves the process
// and nothing here exercises TLS. It would pass with an empty trust store. The honest
// handshake test that would not is TestNewSystemTLS_CompletesRealHandshake in
// tls_handshake_test.go -- see root DESIGN.md, "RULE: A system-TLS constructor must
// reach a real server", clause 2.
func TestNewSystemTLS(t *testing.T) {
	c, err := client.NewSystemTLS("passthrough:///localhost:50051", "test-token")
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestNewWithOpts(t *testing.T) {
	c, err := client.NewWithOpts("passthrough:///localhost:50051", "test-token", client.WithInsecure())
	require.NoError(t, err)
	require.NotNil(t, c)
}

// TestClient_CloseIsIdempotent proves Close can be called more than once
// without error, so a caller can pair an explicit early Close with a
// deferred one (or call Close from more than one goroutine) without
// worrying about a second call panicking or erroring. See root DESIGN.md,
// "RULE: Abandoning a stream must release it" -- every streaming call on
// Client shares one underlying connection, and Close is what lets a caller
// release it deterministically instead of waiting on GC/process exit.
func TestClient_CloseIsIdempotent(t *testing.T) {
	c, err := client.NewWithOpts("passthrough:///localhost:50051", "test-token", client.WithInsecure())
	require.NoError(t, err)

	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}

// TestClient_CloseOnConnectionlessClientIsANoOp proves Close does not panic
// on a Client that never opened a connection. The connection handle is
// unexported, so a zero value -- or a Client a test assembled by hand from
// service stubs -- carries a nil one that no constructor could produce.
// Close is exactly the method such a value reaches, via a `defer c.Close()`
// copied from production code, so a nil deref here would turn a working
// test double into a crash on an unrelated line.
func TestClient_CloseOnConnectionlessClientIsANoOp(t *testing.T) {
	var zero client.Client
	require.NotPanics(t, func() {
		require.NoError(t, zero.Close())
	})

	var nilClient *client.Client
	require.NotPanics(t, func() {
		require.NoError(t, nilClient.Close())
	})
}
