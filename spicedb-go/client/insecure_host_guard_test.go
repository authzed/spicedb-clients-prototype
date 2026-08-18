package client

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
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// authCapturingServer records the "authorization" metadata value observed
// on each incoming CheckBulkPermissions call (which is what Client.Check
// uses under the hood -- see DESIGN.md, "Checks").
type authCapturingServer struct {
	v1.UnimplementedPermissionsServiceServer

	capturedAuth chan string
}

func (s *authCapturingServer) CheckBulkPermissions(ctx context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	auth := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("authorization"); len(vals) > 0 {
			auth = vals[0]
		}
	}
	s.capturedAuth <- auth

	pairs := make([]*v1.CheckBulkPermissionsPair, len(req.GetItems()))
	for i := range req.GetItems() {
		pairs[i] = &v1.CheckBulkPermissionsPair{
			Response: &v1.CheckBulkPermissionsPair_Item{
				Item: &v1.CheckBulkPermissionsResponseItem{
					Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION,
				},
			},
		}
	}
	return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
}

// startAuthCapturingServer starts an in-process (bufconn) gRPC server and
// returns a *counting* dialer -- one that records how many times it was
// actually invoked -- plus the channel of captured "authorization" header
// values. The counting dialer is what proves a rejected construction never
// even attempted to carry a credential onto the wire, not merely that it
// returned an error.
func startAuthCapturingServer(t *testing.T) (dialer func(context.Context, string) (net.Conn, error), dialCount *atomic.Int32, capturedAuth chan string) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &authCapturingServer{capturedAuth: make(chan string, 8)}
	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	var count atomic.Int32
	countingDialer := func(ctx context.Context, _ string) (net.Conn, error) {
		count.Add(1)
		return lis.DialContext(ctx)
	}

	return countingDialer, &count, srv.capturedAuth
}

func testRelationship() rel.Relationship {
	return rel.MustFromTriple("document", "1", "view", "user", "alice", "")
}

// TestNewWithOpts_RefusesInsecureNonLoopbackWithoutOptIn is the idiomatic
// layer's regression test for root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in": WithInsecure against a
// non-loopback endpoint, with no WithInsecureAllowRemoteHost, must fail
// before any credential reaches the wire.
//
// dialCount proves the dialer that would carry the connection (and, over
// it, the bearer token) was never invoked at all -- stronger than "an error
// was returned": an implementation that dialed, sent the token, and only
// then surfaced an error would still fail a bare error check but would
// fail this one.
func TestNewWithOpts_RefusesInsecureNonLoopbackWithoutOptIn(t *testing.T) {
	dialer, dialCount, capturedAuth := startAuthCapturingServer(t)

	c, err := NewWithOpts("passthrough:///evil.example.com:1234", "super-secret-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)

	require.Error(t, err)
	require.Nil(t, c)
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

// TestNewPlaintext_LoopbackWorksWithNoOptIn proves the loopback exemption
// needs no ceremony through the idiomatic constructor: NewPlaintext against
// a loopback endpoint succeeds and actually delivers the bearer token, with
// no WithInsecureAllowRemoteHost involved anywhere.
func TestNewPlaintext_LoopbackWorksWithNoOptIn(t *testing.T) {
	dialer, _, capturedAuth := startAuthCapturingServer(t)

	c, err := NewWithOpts("passthrough:///localhost:50051", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	require.NotNil(t, c)
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.Check(context.Background(), consistency.Full(), "view", testRelationship())
	require.NoError(t, err)

	got := <-capturedAuth
	require.Equal(t, "Bearer test-token", got)
}

// TestNewWithOpts_InsecureAllowRemoteHostSendsToken proves the named
// opt-in actually works: with WithInsecureAllowRemoteHost, an insecure
// connection to a non-loopback endpoint is permitted and the bearer token
// is sent.
func TestNewWithOpts_InsecureAllowRemoteHostSendsToken(t *testing.T) {
	dialer, _, capturedAuth := startAuthCapturingServer(t)

	c, err := NewWithOpts("passthrough:///evil.example.com:1234", "remote-token",
		WithInsecure(),
		WithInsecureAllowRemoteHost(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	require.NotNil(t, c)
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.Check(context.Background(), consistency.Full(), "view", testRelationship())
	require.NoError(t, err)

	got := <-capturedAuth
	require.Equal(t, "Bearer remote-token", got)
}
