package spicedbgoproto

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
)

// TestIsLoopbackEndpoint proves the exact set of gRPC targets this package
// treats as loopback -- the set exempted from WithInsecureAllowRemoteHost
// by root DESIGN.md, "RULE: Credentials over insecure transport require an
// explicit opt-in".
func TestIsLoopbackEndpoint(t *testing.T) {
	loopback := []string{
		"localhost:50051",
		"LOCALHOST:50051",
		"localhost",
		"127.0.0.1:50051",
		"127.0.0.1",
		"127.55.66.77:50051", // any address in 127.0.0.0/8, not just 127.0.0.1
		"[::1]:50051",
		"::1",
		"unix:/var/run/spicedb.sock",
		"unix:///var/run/spicedb.sock",
		"passthrough:///localhost:50051",
		"dns:///127.0.0.1:50051",
	}
	for _, endpoint := range loopback {
		require.True(t, isLoopbackEndpoint(endpoint), "expected %q to be loopback", endpoint)
	}

	notLoopback := []string{
		"example.com:443",
		"staging.internal:443",
		"10.0.0.5:50051", // private, but not loopback
		"8.8.8.8:443",
		"0.0.0.0:50051", // unspecified address, not loopback
		"passthrough:///evil.example.com:1234",
		"dns:///spicedb.prod.example.com:443",
		// Typosquats/lookalikes: a future refactor toward strings.Contains or
		// strings.HasSuffix on "localhost"/"127.0.0.1" would wrongly treat
		// these as loopback and reopen a credential leak. These must stay
		// non-loopback under exact-match host comparison.
		"localhost.evil.com:443",
		"127.0.0.1.evil.com:443",
		"evil-localhost:443",
	}
	for _, endpoint := range notLoopback {
		require.False(t, isLoopbackEndpoint(endpoint), "expected %q to NOT be loopback", endpoint)
	}
}

// capturingPermissionsServer records the "authorization" metadata value it
// observes on each incoming CheckPermission call.
type capturingPermissionsServer struct {
	v1.UnimplementedPermissionsServiceServer

	capturedAuth chan string
}

func (s *capturingPermissionsServer) CheckPermission(ctx context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
	auth := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			auth = vals[0]
		}
	}
	s.capturedAuth <- auth
	return &v1.CheckPermissionResponse{
		Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
	}, nil
}

// startCapturingServer starts an in-process (bufconn) gRPC server and
// returns a *counting* dialer for it -- one that records how many times it
// was actually invoked -- plus the channel of captured "authorization"
// header values. The counting dialer is what lets
// TestNewClient_RefusesInsecureNonLoopbackWithoutOptIn prove the guard
// stops the connection from ever being attempted, not merely that
// NewClient returned an error.
func startCapturingServer(t *testing.T) (dialer func(context.Context, string) (net.Conn, error), dialCount *atomic.Int32, capturedAuth chan string) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &capturingPermissionsServer{capturedAuth: make(chan string, 8)}

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	var count atomic.Int32
	countingDialer := func(ctx context.Context, target string) (net.Conn, error) {
		count.Add(1)
		return lis.DialContext(ctx)
	}

	return countingDialer, &count, srv.capturedAuth
}

// TestNewClient_RefusesInsecureNonLoopbackWithoutOptIn is the regression
// test for root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in": constructing a Client with WithInsecure
// against a non-loopback endpoint, and no WithInsecureAllowRemoteHost, must
// fail before any credential reaches the wire.
//
// The endpoint here ("evil.example.com") isn't merely unreachable -- the
// dialer that WOULD carry the connection (and, over it, the bearer token)
// is wired up via WithDialOptions exactly as a real caller's would be, and
// dialCount proves it was never invoked at all. That is a stronger
// assertion than "NewClient returned an error": an implementation that
// dialed, sent the token, and only THEN surfaced an error would still fail
// a bare error check but would fail this one, because dialCount would be
// nonzero and capturedAuth would have received the token.
func TestNewClient_RefusesInsecureNonLoopbackWithoutOptIn(t *testing.T) {
	dialer, dialCount, capturedAuth := startCapturingServer(t)

	client, err := NewClient("passthrough:///evil.example.com:1234", "super-secret-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)

	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "evil.example.com:1234")
	require.Contains(t, err.Error(), "WithInsecureAllowRemoteHost")

	require.EqualValues(t, 0, dialCount.Load(), "the dialer that would carry the credential to the wire must never be invoked")
	select {
	case got := <-capturedAuth:
		t.Fatalf("server must never have observed a call, but captured authorization metadata %q", got)
	default:
		// Expected: nothing was ever sent, so nothing was ever captured.
	}
}

// TestNewClient_LoopbackWorksWithNoOptIn proves the loopback exemption
// requires no ceremony: an insecure connection to a loopback endpoint
// succeeds, and actually delivers the bearer token, with no
// WithInsecureAllowRemoteHost needed.
func TestNewClient_LoopbackWorksWithNoOptIn(t *testing.T) {
	dialer, _, capturedAuth := startCapturingServer(t)

	client, err := NewClient("passthrough:///localhost:50051", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	require.NotNil(t, client)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.PermissionsServiceClient.CheckPermission(context.Background(), &v1.CheckPermissionRequest{
		Resource:   &v1.ObjectReference{ObjectType: "document", ObjectId: "1"},
		Permission: "view",
		Subject:    &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}},
	})
	require.NoError(t, err)

	got := <-capturedAuth
	require.Equal(t, "Bearer test-token", got)
}

// TestNewClient_InsecureAllowRemoteHostSendsToken proves the named opt-in
// actually works: with WithInsecureAllowRemoteHost, an insecure connection
// to a non-loopback endpoint is permitted and the bearer token is sent.
func TestNewClient_InsecureAllowRemoteHostSendsToken(t *testing.T) {
	dialer, _, capturedAuth := startCapturingServer(t)

	client, err := NewClient("passthrough:///evil.example.com:1234", "remote-token",
		WithInsecure(),
		WithInsecureAllowRemoteHost(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	require.NotNil(t, client)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.PermissionsServiceClient.CheckPermission(context.Background(), &v1.CheckPermissionRequest{
		Resource:   &v1.ObjectReference{ObjectType: "document", ObjectId: "1"},
		Permission: "view",
		Subject:    &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}},
	})
	require.NoError(t, err)

	got := <-capturedAuth
	require.Equal(t, "Bearer remote-token", got)
}
