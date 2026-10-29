package client

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	proto "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// TestEndpointForms_PlaintextURLDialsParsedHost proves an http:// URL is
// accepted by a plaintext constructor, passes the loopback check on its
// parsed host, and reaches a real server listening there.
func TestEndpointForms_PlaintextURLDialsParsedHost(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &authCapturingServer{capturedAuth: make(chan string, 8)}
	gs := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	for _, ep := range []string{lis.Addr().String(), "http://" + lis.Addr().String(), "http://" + lis.Addr().String() + "/"} {
		t.Run(ep, func(t *testing.T) {
			c, err := NewPlaintext(ep, "key")
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })

			res, err := c.CheckBulk(context.Background(), []PermissionCheck{testRequest("1")})
			require.NoError(t, err)
			require.True(t, res[0].HasPermission())
			require.Equal(t, "Bearer key", <-srv.capturedAuth)
		})
	}
}

// TestEndpointForms_TLSURLAccepted covers https:// URLs, with and without a
// port, under the TLS constructor. Construction does not dial.
func TestEndpointForms_TLSURLAccepted(t *testing.T) {
	// A bare TLS host defaults to 443, like an https:// URL.
	for _, ep := range []string{"https://example.com", "https://example.com:8443", "https://example.com/", "example.com"} {
		c, err := NewSystemTLS(ep, "key")
		require.NoError(t, err, ep)
		require.NoError(t, c.Close())
	}
}

// TestEndpointForms_RejectedAtConstruction covers every form that would
// otherwise build a client whose every call fails. Each is refused with
// ErrInvalidArgument naming the problem, before the remote-plaintext policy.
func TestEndpointForms_RejectedAtConstruction(t *testing.T) {
	plaintext := func(ep string) (*Client, error) { return NewPlaintext(ep, "key") }
	tls := func(ep string) (*Client, error) { return NewSystemTLS(ep, "key") }
	cases := []struct {
		name   string
		ctor   func(string) (*Client, error)
		ep     string
		reason string
	}{
		{"http without port", plaintext, "http://localhost", "needs an explicit port"},
		{"https on plaintext", plaintext, "https://example.com", "https:// selects TLS"},
		{"http on TLS", tls, "http://127.0.0.1:50101", "http:// selects plaintext"},
		{"path", plaintext, "http://127.0.0.1:50101/x", "a path"},
		{"path remote", plaintext, "http://example.com:50051/x", "a path"},
		{"userinfo", plaintext, "http://u@127.0.0.1:50101", "userinfo"},
		{"query", tls, "https://example.com?x=1", "a query"},
		{"fragment", tls, "https://example.com#x", "a fragment"},
		{"non-numeric port", tls, "https://example.com:abc", "invalid port"},
		{"non-numeric host:port", tls, "example.com:abc", "not a number"},
		{"unknown scheme", plaintext, "ftp://host:1", `unsupported scheme "ftp"`},
		{"unknown scheme tls", tls, "ftp://host:1", `unsupported scheme "ftp"`},
		{"bare host plaintext", plaintext, "localhost", "a plaintext endpoint needs an explicit port, e.g. localhost:50051"},
		{"bare IPv4 plaintext", plaintext, "127.0.0.1", "needs an explicit port"},
		{"bare IPv6 plaintext", plaintext, "::1", "needs an explicit port"},
		{"bracketed IPv6 plaintext", plaintext, "[::1]", "needs an explicit port"},
		// Registered gRPC resolvers beyond dns, unix and passthrough are
		// refused, not passed through.
		{"unix-abstract target", plaintext, "unix-abstract:spicedb.sock", `unsupported scheme "unix-abstract"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := tc.ctor(tc.ep)
			require.Nil(t, c)
			require.ErrorIs(t, err, ErrInvalidArgument)
			require.ErrorContains(t, err, tc.reason)
			require.ErrorContains(t, err, "expected host:port (e.g. localhost:50051), http://host:port (plaintext), https://host[:port] (TLS), dns:///host:port, unix:/path/to.sock or passthrough:///host:port")
			require.NotContains(t, err.Error(), "WithRemotePlaintextCredentials(RemotePlaintextAllowed)")
		})
	}
}

// TestEndpointForms_RemotePlaintextPolicyOnWellFormedHost: the
// remote-plaintext refusal is reserved for a well-formed remote host.
func TestEndpointForms_RemotePlaintextPolicyOnWellFormedHost(t *testing.T) {
	c, err := NewPlaintext("http://example.com:50051", "key")
	require.Nil(t, c)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorContains(t, err, "non-loopback endpoint")
	// The hint names every option needed, as a caller of this package
	// writes them, and not the proto tier's option.
	require.ErrorIs(t, err, proto.ErrInsecureRemoteHost)
	require.NotContains(t, err.Error(), "WithInsecureAllowRemoteHost")

	// A Docker Compose service name is a remote host like any other.
	_, err = NewPlaintext("spicedb:50051", "key")
	require.EqualError(t, err, `spicedb: failed to create client: refusing to send credentials over plaintext to non-loopback endpoint "spicedb:50051": use TLS (NewSystemTLS), or allow remote plaintext explicitly with NewPlaintext(endpoint, key, WithRemotePlaintextCredentials(RemotePlaintextAllowed)) (with NewWithOpts: WithInsecure() and WithRemotePlaintextCredentials(RemotePlaintextAllowed)): spicedb: insecure connection to a non-loopback host requires an explicit opt-in: spicedb: invalid argument`)
	c, err = NewPlaintext("spicedb:50051", "key", WithRemotePlaintextCredentials(RemotePlaintextAllowed))
	require.NoError(t, err)
	require.NoError(t, c.Close())

	c, err = NewPlaintext("http://example.com:50051", "key",
		WithRemotePlaintextCredentials(RemotePlaintextAllowed))
	require.NoError(t, err)
	require.NoError(t, c.Close())
}
