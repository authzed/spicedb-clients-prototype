package spicedbgoproto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveEndpointAcceptedForms covers the endpoint forms NewClient
// accepts and the gRPC target each one dials. See root DESIGN.md, "Accept
// the endpoint forms deployments use, or reject them at construction".
func TestResolveEndpointAcceptedForms(t *testing.T) {
	cases := []struct {
		endpoint string
		insecure bool
		want     string
	}{
		{"127.0.0.1:50101", true, "127.0.0.1:50101"},
		{"localhost:50051", false, "localhost:50051"},
		{"[::1]:50051", true, "[::1]:50051"},
		{"::1", false, "::1"},
		{"example.com", false, "example.com"},
		{"[::1]", false, "[::1]"},
		{"unix:///var/run/spicedb.sock", true, "unix:///var/run/spicedb.sock"},
		{"dns:///spicedb.example.com:443", false, "dns:///spicedb.example.com:443"},
		{"dns:///localhost:50051", true, "dns:///localhost:50051"},
		{"unix:/var/run/spicedb.sock", true, "unix:/var/run/spicedb.sock"},
		{"passthrough:///localhost:50051", true, "passthrough:///localhost:50051"},
		{"http://127.0.0.1:50101", true, "127.0.0.1:50101"},
		{"HTTP://localhost:50051/", true, "localhost:50051"},
		{"http://[::1]:50051", true, "[::1]:50051"},
		{"https://example.com", false, "example.com:443"},
		{"https://example.com/", false, "example.com:443"},
		{"https://example.com:8443", false, "example.com:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			got, err := resolveEndpoint(tc.endpoint, tc.insecure)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestResolveEndpointRejectedForms covers every malformed form: each fails
// with ErrInvalidEndpoint naming the problem and the accepted forms.
func TestResolveEndpointRejectedForms(t *testing.T) {
	cases := []struct {
		endpoint string
		insecure bool
		reason   string
	}{
		{"http://localhost", true, "needs an explicit port"},
		{"http://127.0.0.1:50101/x", true, `a path ("/x")`},
		{"http://u@127.0.0.1:50101", true, "userinfo"},
		{"http://127.0.0.1:50101?a=b", true, "a query"},
		{"http://127.0.0.1:50101#frag", true, "a fragment"},
		{"http://127.0.0.1:abc", true, "invalid port"},
		{"http://127.0.0.1:70000", true, "out of range"},
		{"http://:50051", true, "the host is missing"},
		{"ftp://host:1", true, "unsupported scheme \"ftp\""},
		{"grpc://host:1", false, "unsupported scheme \"grpc\""},
		// Registered gRPC resolvers other than dns, unix and passthrough are
		// refused rather than passed through unverified.
		{"unix-abstract:spicedb.sock", true, "unsupported scheme \"unix-abstract\""},
		{"unix-abstract:///spicedb.sock", true, "unsupported scheme \"unix-abstract\""},
		{"https://example.com", true, "https:// selects TLS"},
		{"http://127.0.0.1:50101", false, "http:// selects plaintext"},
		{"localhost:abc", true, `the port "abc" is not a number`},
		{"127.0.0.1:50101/x", true, "a path"},
		{"u@127.0.0.1:50101", false, "userinfo"},
		{"", false, "empty"},
		{"localhost", true, "a plaintext endpoint needs an explicit port, e.g. localhost:50051"},
		{"127.0.0.1", true, "needs an explicit port"},
		{"::1", true, "needs an explicit port"},
		{"[::1]", true, "needs an explicit port"},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			_, err := resolveEndpoint(tc.endpoint, tc.insecure)
			require.ErrorIs(t, err, ErrInvalidEndpoint)
			require.ErrorContains(t, err, tc.reason)
			require.ErrorContains(t, err, endpointForms)
		})
	}
}

// TestNewClientChecksFormBeforeRemotePlaintextPolicy pins the ordering: a
// malformed endpoint reports its own problem, and only a well-formed remote
// host gets the remote-plaintext refusal.
func TestNewClientChecksFormBeforeRemotePlaintextPolicy(t *testing.T) {
	for _, ep := range []string{"http://example.com:50051/x", "http://u@example.com:50051", "ftp://example.com:1", "http://example.com"} {
		_, err := NewClient(ep, "t", WithInsecure())
		require.ErrorIs(t, err, ErrInvalidEndpoint, ep)
		require.NotErrorIs(t, err, ErrInsecureRemoteHost, ep)
	}

	_, err := NewClient("http://example.com:50051", "t", WithInsecure())
	require.ErrorIs(t, err, ErrInsecureRemoteHost)
	require.NotErrorIs(t, err, ErrInvalidEndpoint)

	// The loopback check runs on the parsed host.
	c, err := NewClient("http://127.0.0.1:50101", "t", WithInsecure())
	require.NoError(t, err)
	require.NoError(t, c.Close())
}
