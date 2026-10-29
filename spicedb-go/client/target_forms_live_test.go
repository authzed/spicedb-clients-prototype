package client_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/authzed/spicedb-clients/spicedb-go/client"
)

// TestLiveEndpointForms makes a real call over every endpoint form the client
// accepts, against a real SpiceDB: a target the client accepts must work.
//
// It runs when SPICEDB_LIVE_ENDPOINT names a plaintext SpiceDB as
// localhost:port, and covers the unix: forms when SPICEDB_LIVE_UNIX_SOCKET
// names the socket of a SpiceDB started with --grpc-network unix. Both use
// SPICEDB_LIVE_TOKEN (default "somerandomkeyhere").
func TestLiveEndpointForms(t *testing.T) {
	endpoint := os.Getenv("SPICEDB_LIVE_ENDPOINT")
	if endpoint == "" {
		t.Skip("SPICEDB_LIVE_ENDPOINT not set")
	}
	token := os.Getenv("SPICEDB_LIVE_TOKEN")
	if token == "" {
		token = "somerandomkeyhere"
	}
	_, port, err := net.SplitHostPort(endpoint)
	require.NoError(t, err)

	forms := []string{
		"localhost:" + port,
		"127.0.0.1:" + port,
		"http://localhost:" + port,
		"dns:///localhost:" + port,
		"dns:localhost:" + port,
		"passthrough:///127.0.0.1:" + port,
	}
	if sock := os.Getenv("SPICEDB_LIVE_UNIX_SOCKET"); sock != "" {
		forms = append(forms, "unix:"+sock, "unix://"+sock)
	} else {
		t.Log("SPICEDB_LIVE_UNIX_SOCKET not set; unix: forms not exercised")
	}

	const schema = "definition user {}\n\ndefinition document {\n\trelation viewer: user\n}"
	for _, form := range forms {
		t.Run(form, func(t *testing.T) {
			c, err := client.NewPlaintext(form, token)
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			require.NoError(t, c.Ready(ctx, client.WithReadyTimeout(10*time.Second)))
			_, err = c.WriteSchema(ctx, client.WriteSchemaRequest{Schema: schema})
			require.NoError(t, err)
			read, err := c.ReadSchema(ctx, client.ReadSchemaRequest{})
			require.NoError(t, err)
			require.Contains(t, read.Schema, "definition document")
		})
	}
}
