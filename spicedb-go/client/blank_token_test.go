package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// A whitespace-only revision token is rejected wherever a token is required,
// exactly like an empty one: a local ErrInvalidArgument, never a round trip
// carrying a token the server cannot parse.
func TestBlankRevisionTokensAreInvalid(t *testing.T) {
	calls := 0
	c := checkClient(t, &checkServer{single: func(context.Context, *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
		calls++
		return &v1.CheckPermissionResponse{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, CheckedAt: &v1.ZedToken{Token: "r"}}, nil
	}})
	for _, raw := range []string{"", " ", "\t\n "} {
		token := consistency.NewZedToken(raw)
		for _, cs := range []consistency.Strategy{consistency.AtLeast(token), consistency.Snapshot(token)} {
			req := testRequest("a")
			req.Consistency = cs
			_, err := c.Check(context.Background(), req)
			require.ErrorIs(t, err, ErrInvalidArgument, "token %q", raw)
		}

		options := map[string]func(*operationOptions){
			"watch permission sets start": WithWatchPermissionSetsStart(token),
			"lookup permission sets":      WithLookupPermissionSetsRevision(token),
			"download permission sets":    WithDownloadPermissionSetsRevision(token),
		}
		if raw != "" { // the zero token means "start now" for Updates
			options["updates start"] = WithUpdatesStart(token)
		}
		for name, apply := range options {
			var o operationOptions
			apply(&o)
			require.ErrorIs(t, o.err, ErrInvalidArgument, "%s with token %q", name, raw)
		}
	}
	require.Zero(t, calls, "a blank token never reaches the server")

	var o operationOptions
	WithUpdatesStart(consistency.NewZedToken(""))(&o)
	require.NoError(t, o.err, "the zero token still starts Updates at the current revision")
}

// With ItemErrorsReturned, a per-item error is returned on the result, but a
// malformed item in the server's response (an unset permissionship) is not a
// per-item error: it still fails the whole call.
func TestBulkItemErrorsReturnedStillFailsOnMalformedItem(t *testing.T) {
	c := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		out := successfulBulk(r)
		out.Pairs[1].GetItem().Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_UNSPECIFIED
		return out, nil
	}})
	results, err := c.CheckBulk(context.Background(), []PermissionCheck{testRequest("a"), testRequest("b")}, WithBulkItemErrors(ItemErrorsReturned))
	requireMalformed(t, err, "item permissionship is unspecified")
	require.Nil(t, results)
}
