package client

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// A server always sets a permissionship, so an unspecified (or unknown) one
// is a malformed response the client detects: INTERNAL, never a result with
// PermissionshipUnspecified, and never silently dropped by a filter.
func TestUnspecifiedPermissionshipIsMalformed(t *testing.T) {
	ctx := context.Background()
	for _, p := range []v1.CheckPermissionResponse_Permissionship{v1.CheckPermissionResponse_PERMISSIONSHIP_UNSPECIFIED, 99} {
		c := checkClient(t, &checkServer{single: func(context.Context, *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
			return &v1.CheckPermissionResponse{Permissionship: p, CheckedAt: &v1.ZedToken{Token: "r"}}, nil
		}})
		_, err := c.Check(ctx, testRequest("a"))
		requireMalformed(t, err, "permissionship is unspecified")
		require.ErrorContains(t, err, "client detected malformed response from server")
	}

	bulk := checkClient(t, &checkServer{bulk: func(_ context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		out := successfulBulk(r)
		out.Pairs[1].GetItem().Permissionship = v1.CheckPermissionResponse_PERMISSIONSHIP_UNSPECIFIED
		return out, nil
	}})
	_, err := bulk.CheckBulk(ctx, []PermissionCheck{testRequest("a"), testRequest("b")})
	requireMalformed(t, err, "item permissionship is unspecified")
	var be *BulkCheckError
	require.True(t, errors.As(err, &be))
	require.Equal(t, 1, be.Index)

	unspecifiedLookup := &v1.LookupResourcesResponse{ResourceObjectId: "bad", AfterResultCursor: &v1.Cursor{Token: "2"}}
	for _, opts := range [][]LookupResourcesOption{nil, {WithLookupResourcesPermissionshipFilter(LookupHasPermissionOnly)}} {
		srv := &malformedServer{lookup: unspecifiedLookup}
		n, _, err := drain(newMalformedClient(t, srv).LookupResources(ctx, lookupReq(), opts...))
		requireMalformed(t, err, "permissionship is unspecified")
		require.Equal(t, 1, n)
	}

	srv := &malformedServer{subject: &v1.LookupSubjectsResponse{Subject: &v1.ResolvedSubject{SubjectObjectId: "bob"}}}
	n, _, err := drain(newMalformedClient(t, srv).LookupSubjects(ctx, LookupSubjectsRequest{Resource: testObject("document", "1"), Permission: "view", SubjectType: "user", Consistency: consistency.Full()}))
	requireMalformed(t, err, "permissionship is unspecified")
	require.Equal(t, 1, n)
}
