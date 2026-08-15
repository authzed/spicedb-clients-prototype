package client

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
)

// lookupStubServer is a stub PermissionsService whose LookupResources and
// LookupSubjects implementations stream fixed, synthetic responses so tests
// can assert on how the client maps proto responses to native result types.
type lookupStubServer struct {
	v1.UnimplementedPermissionsServiceServer

	lookupResourcesResponses []*v1.LookupResourcesResponse
	lookupSubjectsResponses  []*v1.LookupSubjectsResponse
}

func (s *lookupStubServer) LookupResources(_ *v1.LookupResourcesRequest, stream grpc.ServerStreamingServer[v1.LookupResourcesResponse]) error {
	for _, resp := range s.lookupResourcesResponses {
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	return nil
}

func (s *lookupStubServer) LookupSubjects(_ *v1.LookupSubjectsRequest, stream grpc.ServerStreamingServer[v1.LookupSubjectsResponse]) error {
	for _, resp := range s.lookupSubjectsResponses {
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
	return nil
}

// startLookupStubServer starts an in-process gRPC server backed by bufconn,
// exposing a PermissionsService backed by the given stub, and returns a
// dialer for it.
func startLookupStubServer(t *testing.T, stub *lookupStubServer) func(context.Context, string) (net.Conn, error) {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	grpcServer := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(grpcServer, stub)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// newTestClient dials the given stub server over bufconn and returns a
// ready-to-use *Client.
func newTestClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error)) *Client {
	t.Helper()

	c, err := NewWithOpts("passthrough:///bufnet", "test-token",
		WithInsecure(),
		WithDialOptions(grpc.WithContextDialer(dialer)),
	)
	require.NoError(t, err)
	return c
}

// TestLookupResources_YieldsPermissionshipAndPartialCaveat proves that
// LookupResources surfaces the proto's permissionship and partial caveat
// info instead of dropping them, so a CONDITIONAL match is distinguishable
// from a full HAS_PERMISSION grant.
func TestLookupResources_YieldsPermissionshipAndPartialCaveat(t *testing.T) {
	stub := &lookupStubServer{
		lookupResourcesResponses: []*v1.LookupResourcesResponse{
			{
				ResourceObjectId: "doc1",
				Permissionship:   v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
			},
			{
				ResourceObjectId: "doc2",
				Permissionship:   v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
				PartialCaveatInfo: &v1.PartialCaveatInfo{
					MissingRequiredContext: []string{"ip_address"},
				},
			},
		},
	}
	dialer := startLookupStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []LookupResource
	for r, err := range c.LookupResources(context.Background(), consistency.MinLatency(), "document", "view", "user", "alice") {
		require.NoError(t, err)
		got = append(got, r)
	}

	require.Len(t, got, 2)

	require.Equal(t, "doc1", got[0].ResourceID)
	require.Equal(t, PermissionshipHasPermission, got[0].Permissionship)
	require.Nil(t, got[0].PartialCaveat)

	require.Equal(t, "doc2", got[1].ResourceID)
	require.Equal(t, PermissionshipConditionalPermission, got[1].Permissionship)
	require.NotNil(t, got[1].PartialCaveat)
	require.Equal(t, []string{"ip_address"}, got[1].PartialCaveat.MissingRequiredContext)
}

// TestLookupResources_StreamErrorYieldsZeroValueAndMappedError proves that a
// mid-stream error yields a zero-value LookupResource paired with a native
// mapped error, matching the (result, error) iterator contract used
// elsewhere in the client.
func TestLookupResources_StreamErrorYieldsZeroValueAndMappedError(t *testing.T) {
	// startErroringServer (from errors_test.go) only wires up ReadRelationships;
	// LookupResources hits UnimplementedPermissionsServiceServer, which is
	// enough to prove errors flow through mapGRPCError rather than panicking
	// or being silently swallowed.
	dialer := startErroringServer(t)
	c := newTestClient(t, dialer)

	var (
		yields int
		gotErr error
	)
	for r, err := range c.LookupResources(context.Background(), consistency.MinLatency(), "document", "view", "user", "alice") {
		yields++
		require.Equal(t, LookupResource{}, r)
		gotErr = err
	}

	require.Equal(t, 1, yields)
	require.Error(t, gotErr)
}

// TestLookupSubjects_WildcardSubjectExposesExcludedSubjects is the key
// over-grant-fix assertion: when LookupSubjects resolves a wildcard "*"
// subject, the excluded_subjects the server attaches to that wildcard MUST
// be surfaced to the caller, since dropping them would make a wildcard match
// look like an unconditional grant to every subject including the excluded
// ones.
func TestLookupSubjects_WildcardSubjectExposesExcludedSubjects(t *testing.T) {
	stub := &lookupStubServer{
		lookupSubjectsResponses: []*v1.LookupSubjectsResponse{
			{
				Subject: &v1.ResolvedSubject{
					SubjectObjectId: "*",
					Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
				},
				ExcludedSubjects: []*v1.ResolvedSubject{
					{
						SubjectObjectId: "eve",
						Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
					},
					{
						SubjectObjectId: "mallory",
						Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
					},
				},
			},
		},
	}
	dialer := startLookupStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []LookupSubject
	for s, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		require.NoError(t, err)
		got = append(got, s)
	}

	require.Len(t, got, 1)
	require.Equal(t, "*", got[0].Subject.SubjectID)
	require.Equal(t, PermissionshipHasPermission, got[0].Subject.Permissionship)

	require.Len(t, got[0].ExcludedSubjects, 2)
	excludedIDs := []string{got[0].ExcludedSubjects[0].SubjectID, got[0].ExcludedSubjects[1].SubjectID}
	require.ElementsMatch(t, []string{"eve", "mallory"}, excludedIDs)
}

// TestLookupSubjects_NonWildcardHasNoExcludedSubjects proves that a plain
// (non-wildcard) match doesn't spuriously populate ExcludedSubjects.
func TestLookupSubjects_NonWildcardHasNoExcludedSubjects(t *testing.T) {
	stub := &lookupStubServer{
		lookupSubjectsResponses: []*v1.LookupSubjectsResponse{
			{
				Subject: &v1.ResolvedSubject{
					SubjectObjectId: "alice",
					Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
				},
			},
		},
	}
	dialer := startLookupStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []LookupSubject
	for s, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		require.NoError(t, err)
		got = append(got, s)
	}

	require.Len(t, got, 1)
	require.Equal(t, "alice", got[0].Subject.SubjectID)
	require.Empty(t, got[0].ExcludedSubjects)
}

// TestLookupSubjects_FallsBackToDeprecatedFields proves that when a server
// only populates the deprecated top-level subject_object_id (and leaves the
// non-deprecated `subject` field unset), the client still surfaces a usable
// ResolvedSubject rather than an empty SubjectID.
func TestLookupSubjects_FallsBackToDeprecatedFields(t *testing.T) {
	stub := &lookupStubServer{
		lookupSubjectsResponses: []*v1.LookupSubjectsResponse{
			{
				SubjectObjectId: "bob",                                                        //nolint:staticcheck
				Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, //nolint:staticcheck
			},
		},
	}
	dialer := startLookupStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []LookupSubject
	for s, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		require.NoError(t, err)
		got = append(got, s)
	}

	require.Len(t, got, 1)
	require.Equal(t, "bob", got[0].Subject.SubjectID)
	require.Equal(t, PermissionshipHasPermission, got[0].Subject.Permissionship)
}

// TestLookupSubjects_ExcludedSubjectsFallsBackToDeprecatedIds proves that
// when a server populates a wildcard "*" match's exclusions ONLY via the
// deprecated top-level excluded_subject_ids field (leaving the
// non-deprecated excluded_subjects list empty), the client still surfaces
// those exclusions as ResolvedSubjects. This is security-relevant: dropping
// exclusions from an older-wire-format server would silently over-grant
// access to the excluded subjects. Removing the excluded_subject_ids
// fallback branch in LookupSubjects MUST fail this test.
func TestLookupSubjects_ExcludedSubjectsFallsBackToDeprecatedIds(t *testing.T) {
	stub := &lookupStubServer{
		lookupSubjectsResponses: []*v1.LookupSubjectsResponse{
			{
				Subject: &v1.ResolvedSubject{
					SubjectObjectId: "*",
					Permissionship:  v1.LookupPermissionship_LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
				},
				// Non-deprecated excluded_subjects deliberately left empty;
				// only the deprecated excluded_subject_ids is populated.
				ExcludedSubjectIds: []string{"eve", "mallory"}, //nolint:staticcheck
			},
		},
	}
	dialer := startLookupStubServer(t, stub)
	c := newTestClient(t, dialer)

	var got []LookupSubject
	for s, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		require.NoError(t, err)
		got = append(got, s)
	}

	require.Len(t, got, 1)
	require.Equal(t, "*", got[0].Subject.SubjectID)
	require.Len(t, got[0].ExcludedSubjects, 2)
	require.Equal(t, []ResolvedSubject{
		{SubjectID: "eve"},
		{SubjectID: "mallory"},
	}, got[0].ExcludedSubjects)
}

// TestLookupSubjects_StreamErrorYieldsZeroValueAndMappedError proves the
// (result, error) iterator contract holds for LookupSubjects too.
func TestLookupSubjects_StreamErrorYieldsZeroValueAndMappedError(t *testing.T) {
	dialer := startErroringServer(t)
	c := newTestClient(t, dialer)

	var (
		yields int
		gotErr error
	)
	for s, err := range c.LookupSubjects(context.Background(), consistency.MinLatency(), "document", "doc1", "view", "user") {
		yields++
		require.Equal(t, LookupSubject{}, s)
		gotErr = err
	}

	require.Equal(t, 1, yields)
	require.Error(t, gotErr)
}
