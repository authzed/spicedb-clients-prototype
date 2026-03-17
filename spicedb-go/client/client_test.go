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
