package client

import (
	"context"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"testing"
	"time"
)

type blockedReadServer struct {
	v1.UnimplementedPermissionsServiceServer
	started, stopped chan struct{}
}

func (s *blockedReadServer) ReadRelationships(_ *v1.ReadRelationshipsRequest, stream grpc.ServerStreamingServer[v1.ReadRelationshipsResponse]) error {
	close(s.started)
	<-stream.Context().Done()
	close(s.stopped)
	return stream.Context().Err()
}
func TestParentCancellationInterruptsPendingReceiveAndReleasesServer(t *testing.T) {
	s := &blockedReadServer{started: make(chan struct{}), stopped: make(chan struct{})}
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	c := newTestClient(t, func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		for _, e := range c.ReadRelationships(ctx, ReadRelationshipsRequest{Filter: testFilter("document"), Consistency: consistency.Full()}) {
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	<-s.started
	cancel()
	select {
	case e := <-done:
		require.ErrorIs(t, e, context.Canceled)
		require.ErrorIs(t, e, ErrCanceled)
	case <-time.After(time.Second):
		t.Fatal("pending stream receive did not cancel")
	}
	select {
	case <-s.stopped:
	case <-time.After(time.Second):
		t.Fatal("server stream did not terminate")
	}
}
