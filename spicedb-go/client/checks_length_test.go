package client

import (
	"context"
	"fmt"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
)

func TestCheckBulkRejectsMalformedPairs(t *testing.T) {
	for _, mode := range []string{"short", "long", "nil", "missing request", "wrong request", "no response", "duplicate", "misordered"} {
		t.Run(mode, func(t *testing.T) {
			c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
				out := successfulBulk(r)
				switch mode {
				case "short":
					out.Pairs = out.Pairs[:1]
				case "long":
					out.Pairs = append(out.Pairs, out.Pairs[0])
				case "nil":
					out.Pairs[0] = nil
				case "missing request":
					out.Pairs[0].Request = nil
				case "wrong request":
					out.Pairs[0].Request.Resource.ObjectId = "other"
				case "no response":
					out.Pairs[0].Response = nil
				case "duplicate":
					out.Pairs[1] = out.Pairs[0]
				case "misordered":
					out.Pairs[0], out.Pairs[1] = out.Pairs[1], out.Pairs[0]
				}
				return out, nil
			}})
			_, e := c.CheckBulk(context.Background(), []PermissionCheck{testRequest("a"), testRequest("b")})
			require.Error(t, e)
			var native *Error
			require.ErrorAs(t, e, &native)
			require.Equal(t, CodeInternal, native.Code)
			require.ErrorContains(t, e, "client detected malformed response from server")
		})
	}
}
func TestCheckBulkLengthGuardOnLaterChunk(t *testing.T) {
	calls := 0
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		calls++
		out := successfulBulk(r)
		if calls == 2 {
			out.Pairs = nil
		}
		return out, nil
	}})
	rs := make([]PermissionCheck, 1001)
	for i := range rs {
		rs[i] = testRequest(fmt.Sprint(i))
	}
	_, e := c.CheckBulk(context.Background(), rs)
	require.Error(t, e)
	require.Contains(t, e.Error(), "1000")
	require.Equal(t, 2, calls)
}
