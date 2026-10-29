package spicedbgoproto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
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

func TestNewClientFromConnBorrowsWithoutOwning(t *testing.T) {
	conn, err := grpc.NewClient("passthrough:///unused", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	c := NewClientFromConn(conn)
	require.NotNil(t, c.PermissionsServiceClient)
	require.Same(t, conn, c.Connection())
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.NotEqual(t, connectivity.Shutdown, conn.GetState(), "Close must not close a borrowed connection")
}
