package spicedbgoproto

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewClient(t *testing.T) {
	client, err := NewClient("passthrough:///localhost:50051", "test-token", WithInsecure())
	require.NoError(t, err)

	require.NotNil(t, client.PermissionsServiceClient)
	require.NotNil(t, client.SchemaServiceClient)
	require.NotNil(t, client.WatchServiceClient)
	require.NotNil(t, client.ExperimentalServiceClient)
}

func TestWithInsecure(t *testing.T) {
	cfg := &clientConfig{}
	WithInsecure()(cfg)
	require.True(t, cfg.insecure)
}

func TestWithDialOptions(t *testing.T) {
	cfg := &clientConfig{}
	opt := grpc.WithAuthority("custom-authority")
	WithDialOptions(opt)(cfg)
	require.Len(t, cfg.dialOptions, 1)
}
