package client

import (
	"context"
	"net"
	"testing"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func startReadyServer(t *testing.T, hs *health.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gs := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(gs, v1.UnimplementedPermissionsServiceServer{})
	if hs != nil {
		healthpb.RegisterHealthServer(gs, hs)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

func TestReady_ServingEndpoint(t *testing.T) {
	c, err := NewPlaintext(startReadyServer(t, health.NewServer()), "key")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Ready(context.Background(), WithReadyTimeout(5*time.Second)))
}

// A server without the health service answered, so it is reachable.
func TestReady_ServerWithoutHealthServiceIsReachable(t *testing.T) {
	c, err := NewPlaintext(startReadyServer(t, nil), "key")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Ready(context.Background(), WithReadyTimeout(5*time.Second)))
}

// Ready waits through a not-yet-serving server, and gives up at the deadline
// with the last status when it never serves.
func TestReady_WaitsForServing(t *testing.T) {
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	c, err := NewPlaintext(startReadyServer(t, hs), "key")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	err = c.Ready(context.Background(), WithReadyTimeout(300*time.Millisecond))
	require.Equal(t, CodeDeadlineExceeded, CodeOf(err), "%v", err)
	require.ErrorContains(t, err, "NOT_SERVING")

	go func() {
		time.Sleep(200 * time.Millisecond)
		hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	}()
	require.NoError(t, c.Ready(context.Background(), WithReadyTimeout(5*time.Second)))
}

// An unreachable endpoint fails within the timeout with the client's
// DEADLINE_EXCEEDED shape, naming the endpoint and the root cause.
func TestReady_UnreachableEndpoint(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	c, err := NewPlaintext(addr, "key")
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	start := time.Now()
	err = c.Ready(context.Background(), WithReadyTimeout(500*time.Millisecond))
	require.Less(t, time.Since(start), 3*time.Second)
	var native *Error
	require.ErrorAs(t, err, &native)
	require.Equal(t, CodeDeadlineExceeded, native.Code)
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, addr)
	require.ErrorContains(t, err, "connection refused")

	// The client default deadline bounds Ready when no timeout is given.
	c2, err := NewPlaintext(addr, "key", WithDefaultTimeout(300*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })
	require.Equal(t, CodeDeadlineExceeded, CodeOf(c2.Ready(context.Background())))

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err = c.Ready(ctx)
	require.Equal(t, CodeCanceled, CodeOf(err), "%v", err)
	require.ErrorIs(t, err, context.Canceled)
}
