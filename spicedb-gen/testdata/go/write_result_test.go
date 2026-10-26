package permissions_test

import (
	"context"
	"net"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	. "github.com/authzed/spicedb-clients/spicedb-gen/testdata/go"
	"github.com/authzed/spicedb-clients/spicedb-go/client"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

type writeResultServer struct {
	v1.UnimplementedPermissionsServiceServer
}

func (*writeResultServer) WriteRelationships(context.Context, *v1.WriteRelationshipsRequest) (*v1.WriteRelationshipsResponse, error) {
	return &v1.WriteRelationshipsResponse{WrittenAt: &v1.ZedToken{Token: "opaque-write-revision"}}, nil
}

func TestTypedWritesPreserveNativeResult(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(server, &writeResultServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	native, err := client.NewWithOpts("passthrough:///bufnet", "test-token", client.WithInsecure(), client.WithRemotePlaintextCredentials(client.RemotePlaintextAllowed), client.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) })))
	require.NoError(t, err)
	t.Cleanup(func() { _ = native.Close() })
	typed := NewTypedClient(native)
	for name, write := range map[string]func(context.Context, []TypedRelationship, ...client.WriteOption) (client.WriteResult, error){"touch": typed.Touch, "create": typed.Create, "delete": typed.Delete} {
		t.Run(name, func(t *testing.T) {
			result, err := write(context.Background(), []TypedRelationship{Document("doc").Viewer(User("alice"))})
			require.NoError(t, err)
			require.Equal(t, consistency.NewZedToken("opaque-write-revision"), result.Revision)
			require.Equal(t, "opaque-write-revision", result.Revision.String())
			result, err = write(context.Background(), []TypedRelationship{Document("").Viewer(User("alice"))})
			require.Error(t, err)
			require.Equal(t, client.WriteResult{}, result)
		})
	}
}
