package client

import (
	"context"
	"net"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// checkFailFirstCapturingServer records the item count of every
// CheckBulkPermissions request it receives, and fails the first N calls with
// a PER-ITEM error on every pair (not a top-level RPC error -- grpc-go's
// service-config retryPolicy from Task 2 would silently absorb a top-level
// UNAVAILABLE/ABORTED before CheckWithContext ever saw it, defeating this
// test). A per-item error still makes CheckWithContext return a non-nil
// error (see checks.go's `if errResp := pair.GetError(); errResp != nil`),
// exactly like the top-level case, but the RPC itself succeeds at the
// transport level so grpc-go's transparent retry never engages. Succeeds on
// every call after failFirstN. Models "one transient error" mid-iteration --
// the scenario CheckIterWithContext's flush must survive without resending a
// growing batch.
type checkFailFirstCapturingServer struct {
	v1.UnimplementedPermissionsServiceServer

	failFirstN  int
	callCount   int
	requestSize []int
}

func (s *checkFailFirstCapturingServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	s.callCount++
	s.requestSize = append(s.requestSize, len(req.GetItems()))

	pairs := make([]*v1.CheckBulkPermissionsPair, len(req.GetItems()))
	if s.callCount <= s.failFirstN {
		for i := range req.GetItems() {
			pairs[i] = &v1.CheckBulkPermissionsPair{
				Response: &v1.CheckBulkPermissionsPair_Error{
					Error: &status.Status{
						Code:    int32(codes.Internal),
						Message: "simulated per-item failure",
					},
				},
			}
		}
		return &v1.CheckBulkPermissionsResponse{Pairs: pairs}, nil
	}

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

func startCheckFailFirstCapturingServer(t *testing.T, failFirstN int) (*checkFailFirstCapturingServer, func(context.Context, string) (net.Conn, error)) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := &checkFailFirstCapturingServer{failFirstN: failFirstN}
	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, srv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return srv, func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestCheckIterWithContext_ConsumerContinuingPastErrorDoesNotResendGrowingBatch
// is the regression test for the flush bug: CheckIterWithContext's flush
// closure returned on the error path (`return yield(CheckResult{}, err)`)
// BEFORE clearing the batch. Go's iterator contract says yield returning
// true means "keep going", so a consumer that logs the error and continues
// left the failed batch populated -- the next relationship pushed it past
// defaultCheckBatchSize, and flush resent the SAME failing batch plus one,
// growing on every subsequent element instead of starting a fresh batch.
//
// Feeds defaultCheckBatchSize+2 relationships with the server configured to
// fail only the FIRST call (one transient error). A consumer that continues
// past the error must see: [defaultCheckBatchSize items (fails), 2 items
// (succeeds)] on the wire -- not a second call whose size has grown past
// defaultCheckBatchSize, which would mean the first (failed) batch's items
// were silently resent.
func TestCheckIterWithContext_ConsumerContinuingPastErrorDoesNotResendGrowingBatch(t *testing.T) {
	srv, dialer := startCheckFailFirstCapturingServer(t, 1)

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		// bufnet is an in-memory bufconn dial target, not a real network
		// destination -- unrelated to what this test exercises.
		WithInsecureAllowRemoteHost(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)

	total := defaultCheckBatchSize + 2
	rels := make([]rel.Relationship, total)
	for i := range rels {
		rels[i] = rel.MustFromTriple("document", "doc", "view", "user", "u", "")
	}

	var sawError bool
	var yieldedResults int
	for _, checkErr := range c.CheckIterWithContext(context.Background(), consistency.MinLatency(), "view", nil, slices.Values(rels)) {
		if checkErr != nil {
			sawError = true
			// A consumer that logs the error and continues -- exactly the
			// idiom the bug report describes as the one that never noticed
			// this defect (a consumer that `break`s on error never triggers
			// it).
			continue
		}
		yieldedResults++
	}

	require.True(t, sawError, "the first (failing) batch must surface its error to the consumer")

	require.Equal(t, []int{defaultCheckBatchSize, 2}, srv.requestSize,
		"the second request must contain only the items accumulated AFTER the failed batch was "+
			"cleared, not the failed batch's items plus the new ones (which would mean the batch "+
			"was never cleared on the error path)")

	require.Equal(t, 2, yieldedResults, "only the second (successful) batch's results should be yielded")
}
