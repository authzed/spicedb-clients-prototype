package client

import (
	"context"
	"fmt"
	"slices"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/stretchr/testify/require"
)

func TestCheckBulkChunkingOrderAndPerChunkRevision(t *testing.T) {
	for _, count := range []int{0, 1, 999, 1000, 1001, 2501} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			sizes := []int{}
			c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
				sizes = append(sizes, len(r.Items))
				require.LessOrEqual(t, len(r.Items), 1000)
				response := successfulBulk(r)
				response.CheckedAt.Token = fmt.Sprint(len(sizes))
				for i, p := range response.Pairs {
					id := r.Items[i].Resource.ObjectId
					require.Equal(t, fmt.Sprint((len(sizes)-1)*1000+i), id)
					if i%2 == 1 {
						p.GetItem().Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_CONDITIONAL_PERMISSION
						p.GetItem().PartialCaveatInfo = &v1.PartialCaveatInfo{MissingRequiredContext: []string{"needed"}}
					}
				}
				return response, nil
			}})
			rs := make([]PermissionCheck, count)
			for i := range rs {
				rs[i] = testRequest(fmt.Sprint(i))
			}
			out, e := c.CheckBulk(context.Background(), rs)
			require.NoError(t, e)
			require.Len(t, out, count)
			require.Len(t, sizes, (count+999)/1000)
			for i, r := range out {
				require.EqualValues(t, fmt.Sprint(i/1000+1), r.CheckedAt.String())
				require.Equal(t, i%1000%2 == 0, r.HasPermission())
				if !r.HasPermission() {
					require.Equal(t, []string{"needed"}, r.MissingContext)
				}
			}
		})
	}
}
func TestCheckBulkRejectsMixedConsistencyBeforeAnyChunk(t *testing.T) {
	c := &Client{}
	rs := make([]PermissionCheck, 1001)
	for i := range rs {
		rs[i] = testRequest(fmt.Sprint(i))
	}
	for _, strategy := range []consistency.Strategy{consistency.MinLatency(), consistency.AtLeast(consistency.NewZedToken("token")), consistency.Snapshot(consistency.NewZedToken("token")), {}} {
		rs[1000].Consistency = strategy
		_, e := c.CheckBulk(context.Background(), rs)
		require.ErrorIs(t, e, ErrInvalidArgument)
		var item *BulkCheckError
		require.ErrorAs(t, e, &item)
		require.Equal(t, 1000, item.Index)
	}
}
func TestCheckIterBatchesAndPreservesContext(t *testing.T) {
	sizes := []int{}
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		sizes = append(sizes, len(r.Items))
		for _, item := range r.Items {
			require.Equal(t, "eu", item.Context.AsMap()["region"])
		}
		return successfulBulk(r), nil
	}})
	rs := make([]PermissionCheck, 2003)
	for i := range rs {
		rs[i] = testRequest(fmt.Sprint(i))
	}
	count := 0
	for r, e := range c.CheckIter(context.Background(), slices.Values(rs), WithCheckContext(map[string]any{"region": "eu"})) {
		require.NoError(t, e)
		require.True(t, r.HasPermission())
		count++
	}
	require.Equal(t, len(rs), count)
	require.Equal(t, []int{1000, 1000, 3}, sizes)
}
