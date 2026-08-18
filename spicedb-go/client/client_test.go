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
