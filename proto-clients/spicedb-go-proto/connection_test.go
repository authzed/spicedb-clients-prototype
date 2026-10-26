package spicedbgoproto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/connectivity"
)

func TestConnectionBorrowsExistingTransport(t *testing.T) {
	c, err := NewClient("localhost:50051", "test-token", WithInsecure())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Same(t, c.conn, c.Connection())
	require.Same(t, c.Connection(), c.Connection())
	require.NoError(t, c.Close())
	require.Equal(t, connectivity.Shutdown, c.conn.GetState())
}

func TestConnectionWithoutTransportReturnsNil(t *testing.T) {
	require.Nil(t, (&Client{}).Connection())
	var c *Client
	require.Nil(t, c.Connection())
}
