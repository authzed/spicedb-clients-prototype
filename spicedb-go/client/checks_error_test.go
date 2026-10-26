package client

import (
	"context"
	"fmt"
	"testing"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCheckBulkItemErrorsCarryGlobalIndexAndTypedSource(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			calls := 0
			detailed, e := status.New(codes.InvalidArgument, "bad input").WithDetails(&errdetails.ErrorInfo{Reason: "REASON", Domain: "authzed.com", Metadata: map[string]string{"key": "value"}})
			require.NoError(t, e)
			c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
				calls++
				out := successfulBulk(r)
				if calls == 2 {
					if malformed {
						out.Pairs[1].Response = nil
					} else {
						out.Pairs[1].Response = &v1.CheckBulkPermissionsPair_Error{Error: (*rpcstatus.Status)(detailed.Proto())}
					}
				}
				return out, nil
			}})
			rs := make([]PermissionCheck, 1003)
			for i := range rs {
				rs[i] = testRequest(fmt.Sprint(i))
			}
			_, e = c.CheckBulk(context.Background(), rs)
			require.Error(t, e)
			var item *BulkCheckError
			require.ErrorAs(t, e, &item)
			require.Equal(t, 1001, item.Index)
			if !malformed {
				require.ErrorIs(t, e, ErrInvalidArgument)
				var native *Error
				require.ErrorAs(t, e, &native)
				require.Equal(t, "REASON", native.Reason)
				require.Equal(t, "authzed.com", native.ReasonDomain)
				require.Equal(t, "value", native.ReasonMetadata["key"])
				require.Equal(t, codes.InvalidArgument, status.Code(native.Unwrap()))
			}
		})
	}
}
