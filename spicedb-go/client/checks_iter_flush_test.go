package client

import (
	"context"
	"slices"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCheckIterFailureTerminatesWithoutGrowingOrReplayingBatch(t *testing.T) {
	calls := 0
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		calls++
		require.Len(t, r.Items, 1000)
		return nil, status.Error(codes.InvalidArgument, "bad chunk")
	}})
	rs := make([]PermissionCheck, 1002)
	for i := range rs {
		rs[i] = testRequest("a")
	}
	errors := 0
	for _, e := range c.CheckIter(context.Background(), slices.Values(rs)) {
		require.ErrorIs(t, e, ErrInvalidArgument)
		errors++
	}
	require.Equal(t, 1, errors)
	require.Equal(t, 1, calls)
}
