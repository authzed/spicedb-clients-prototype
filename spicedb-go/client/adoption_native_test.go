package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/authzed/spicedb-clients/spicedb-go/consistency"
	"github.com/authzed/spicedb-clients/spicedb-go/rel"
)

// adoptionServer is a scriptable fake for the native behaviors added for
// zed's migration: retries, deadlines, import aborts, bulk item errors,
// watch kinds and delete modes.
type adoptionServer struct {
	v1.UnimplementedPermissionsServiceServer
	v1.UnimplementedSchemaServiceServer
	v1.UnimplementedWatchServiceServer

	mu            sync.Mutex
	readSchema    atomic.Int32
	readSchemaErr codes.Code
	watchCalls    atomic.Int32
	watchKinds    []v1.WatchKind
	importBatches []int
	deletes       []*v1.DeleteRelationshipsRequest
	deletePages   []*v1.DeleteRelationshipsResponse
	schemaDelay   time.Duration
}

func (s *adoptionServer) ReadSchema(ctx context.Context, _ *v1.ReadSchemaRequest) (*v1.ReadSchemaResponse, error) {
	s.readSchema.Add(1)
	if s.schemaDelay > 0 {
		select {
		case <-time.After(s.schemaDelay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if s.readSchemaErr != codes.OK {
		return nil, status.Error(s.readSchemaErr, "nope")
	}
	return &v1.ReadSchemaResponse{SchemaText: "definition user {}", ReadAt: &v1.ZedToken{Token: "r"}}, nil
}

func (s *adoptionServer) Watch(req *v1.WatchRequest, stream grpc.ServerStreamingServer[v1.WatchResponse]) error {
	s.watchCalls.Add(1)
	s.mu.Lock()
	s.watchKinds = req.GetOptionalUpdateKinds()
	s.mu.Unlock()
	if s.watchCalls.Load() == 1 && len(s.watchKinds) == 0 {
		return status.Error(codes.Unavailable, "flaky")
	}
	return stream.Send(&v1.WatchResponse{ChangesThrough: &v1.ZedToken{Token: "w"}, SchemaUpdated: true})
}

func (s *adoptionServer) ImportBulkRelationships(stream grpc.ClientStreamingServer[v1.ImportBulkRelationshipsRequest, v1.ImportBulkRelationshipsResponse]) error {
	var total uint64
	for {
		req, err := stream.Recv()
		if err != nil {
			return stream.SendAndClose(&v1.ImportBulkRelationshipsResponse{NumLoaded: total})
		}
		s.mu.Lock()
		s.importBatches = append(s.importBatches, len(req.GetRelationships()))
		s.mu.Unlock()
		for _, r := range req.GetRelationships() {
			if r.GetResource().GetObjectId() == "dup" {
				return status.Error(codes.AlreadyExists, "duplicate relationship")
			}
		}
		total += uint64(len(req.GetRelationships()))
	}
}

func (s *adoptionServer) CheckBulkPermissions(_ context.Context, req *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
	resp := &v1.CheckBulkPermissionsResponse{CheckedAt: &v1.ZedToken{Token: "c"}}
	for _, item := range req.GetItems() {
		pair := &v1.CheckBulkPermissionsPair{Request: item}
		if item.GetResource().GetObjectId() == "broken" {
			pair.Response = &v1.CheckBulkPermissionsPair_Error{Error: &rpcstatus.Status{Code: int32(codes.FailedPrecondition), Message: "no such permission"}}
		} else {
			pair.Response = &v1.CheckBulkPermissionsPair_Item{Item: &v1.CheckBulkPermissionsResponseItem{Permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION}}
		}
		resp.Pairs = append(resp.Pairs, pair)
	}
	return resp, nil
}

func (s *adoptionServer) DeleteRelationships(_ context.Context, req *v1.DeleteRelationshipsRequest) (*v1.DeleteRelationshipsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, req)
	if len(s.deletePages) > 0 {
		page := s.deletePages[0]
		s.deletePages = s.deletePages[1:]
		return page, nil
	}
	return &v1.DeleteRelationshipsResponse{DeletedAt: &v1.ZedToken{Token: "d"}, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE}, nil
}

func newAdoptionClient(t *testing.T, srv *adoptionServer, opts ...Option) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	g := grpc.NewServer()
	v1.RegisterPermissionsServiceServer(g, srv)
	v1.RegisterSchemaServiceServer(g, srv)
	v1.RegisterWatchServiceServer(g, srv)
	go func() { _ = g.Serve(listener) }()
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	c, err := NewFromConn(conn, opts...)
	require.NoError(t, err)
	return c
}

func TestImportReportsServerAbortNotEOF(t *testing.T) {
	c := newAdoptionClient(t, &adoptionServer{})
	rels := func(yield func(rel.Relationship) bool) {
		// Enough batches that a later Send observes the aborted stream.
		for i := 0; i < 50; i++ {
			id := "ok"
			if i == 0 {
				id = "dup"
			}
			if !yield(testTriple("document", id, "viewer", "user", "alice", "")) {
				return
			}
		}
	}
	_, err := c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(1))
	require.ErrorIs(t, err, ErrAlreadyExists)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestImportBatchSize(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	rels := func(yield func(rel.Relationship) bool) {
		for i := 0; i < 7; i++ {
			if !yield(testTriple("document", "ok", "viewer", "user", "alice", "")) {
				return
			}
		}
	}
	result, err := c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(3))
	require.NoError(t, err)
	require.EqualValues(t, 7, result.ImportedCount)
	require.Equal(t, []int{3, 3, 1}, srv.importBatches)
	_, err = c.ImportRelationships(context.Background(), ImportRelationshipsRequest{Relationships: rels}, WithImportRelationshipsBatchSize(0))
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestRetryPolicyIsConfigurable(t *testing.T) {
	ctx := context.Background()

	srv := &adoptionServer{readSchemaErr: codes.Unavailable}
	_, err := newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 1, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond})).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrUnavailable)
	require.EqualValues(t, 2, srv.readSchema.Load(), "one attempt plus one retry")

	srv = &adoptionServer{readSchemaErr: codes.Unavailable}
	_, err = newAdoptionClient(t, srv, WithRetries(RetriesDisabled)).ReadSchema(ctx, ReadSchemaRequest{})
	require.Error(t, err)
	require.EqualValues(t, 1, srv.readSchema.Load(), "disabled retries make one attempt")

	var events []RetryEvent
	srv = &adoptionServer{readSchemaErr: codes.Internal}
	_, err = newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 2, RetryableCodes: []ErrorCode{CodeInternal}, InitialBackoff: time.Millisecond,
		OnRetry: func(e RetryEvent) { events = append(events, e) }})).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrInternal)
	require.EqualValues(t, 3, srv.readSchema.Load())
	require.Len(t, events, 2)
	require.Equal(t, "read schema", events[0].Operation)
	require.Equal(t, 1, events[0].Attempt)
	require.ErrorIs(t, events[0].Err, ErrInternal)

	_, err = NewFromConn(&grpc.ClientConn{}, WithRetryPolicy(RetryPolicy{MaxRetries: 1}))
	require.ErrorIs(t, err, ErrInvalidArgument, "retries need a positive backoff")
}

func TestStreamRetriesCanBeDisabled(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv, WithRetryPolicy(RetryPolicy{MaxRetries: 3, RetryableCodes: []ErrorCode{CodeUnavailable}, InitialBackoff: time.Millisecond}))
	for _, err := range c.Updates(context.Background(), UpdatesRequest{}) {
		require.NoError(t, err)
		break
	}
	require.EqualValues(t, 2, srv.watchCalls.Load(), "enabled: the failed open is retried")

	srv = &adoptionServer{}
	c = newAdoptionClient(t, srv, WithStreamRetries(StreamRetriesDisabled))
	for _, err := range c.Updates(context.Background(), UpdatesRequest{}) {
		require.ErrorIs(t, err, ErrUnavailable)
		break
	}
	require.EqualValues(t, 1, srv.watchCalls.Load(), "disabled: one open")
}

func TestDefaultDeadlineCanBeDisabled(t *testing.T) {
	srv := &adoptionServer{schemaDelay: 150 * time.Millisecond}
	_, err := newAdoptionClient(t, srv, WithDefaultTimeout(50*time.Millisecond)).ReadSchema(context.Background(), ReadSchemaRequest{})
	require.ErrorIs(t, err, ErrDeadlineExceeded)

	_, err = newAdoptionClient(t, srv, WithDefaultTimeout(50*time.Millisecond), WithDefaultDeadline(DefaultDeadlineDisabled)).ReadSchema(context.Background(), ReadSchemaRequest{})
	require.NoError(t, err, "no client deadline")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = newAdoptionClient(t, srv, WithDefaultDeadline(DefaultDeadlineDisabled)).ReadSchema(ctx, ReadSchemaRequest{})
	require.ErrorIs(t, err, context.DeadlineExceeded, "the caller's deadline still applies")
}

func TestBulkItemErrorsReturned(t *testing.T) {
	c := newAdoptionClient(t, &adoptionServer{})
	requests := []PermissionCheck{
		testRequest("ok-1"),
		testRequest("broken"),
		{Resource: rel.ObjectRef{}, Permission: "view", Consistency: consistency.Full()}, // invalid locally
		testRequest("ok-2"),
	}

	_, err := c.CheckBulk(context.Background(), requests)
	var itemErr *BulkCheckError
	require.ErrorAs(t, err, &itemErr, "default mode fails the call")

	results, err := c.CheckBulk(context.Background(), requests, WithBulkItemErrors(ItemErrorsReturned))
	require.NoError(t, err)
	require.Len(t, results, 4)
	require.True(t, results[0].HasPermission())
	require.True(t, results[3].HasPermission())
	for i, want := range map[int]error{1: ErrFailedPrecondition, 2: ErrInvalidArgument} {
		require.False(t, results[i].HasPermission(), "an errored item never grants")
		require.ErrorAs(t, results[i].Err, &itemErr)
		require.Equal(t, i, itemErr.Index)
		require.ErrorIs(t, results[i].Err, want)
	}

	var iterResults []CheckResult
	for r, err := range c.CheckIter(context.Background(), func(yield func(PermissionCheck) bool) {
		for _, r := range requests {
			if !yield(r) {
				return
			}
		}
	}, WithBulkItemErrors(ItemErrorsReturned)) {
		require.NoError(t, err)
		iterResults = append(iterResults, r)
	}
	require.Len(t, iterResults, 4)
	for i := range requests {
		require.Equal(t, results[i].HasPermission(), iterResults[i].HasPermission(), "item %d", i)
		require.Equal(t, results[i].Err != nil, iterResults[i].Err != nil, "item %d", i)
	}
}

func TestWatchSchemaChanges(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	for event, err := range c.Updates(context.Background(), UpdatesRequest{}, WithUpdatesSchemaChanges(SchemaChangesIncluded), WithUpdatesCheckpoints(CheckpointsIncluded)) {
		require.NoError(t, err)
		require.True(t, event.SchemaUpdated)
		break
	}
	require.ElementsMatch(t, []v1.WatchKind{
		v1.WatchKind_WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES,
		v1.WatchKind_WATCH_KIND_INCLUDE_CHECKPOINTS,
		v1.WatchKind_WATCH_KIND_INCLUDE_SCHEMA_UPDATES,
	}, srv.watchKinds)
}

func TestAllOrNothingDeleteAndProgress(t *testing.T) {
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	ctx := context.Background()

	_, err := c.DeleteRelationships(ctx, DeleteRequest{Filter: testFilter("document")})
	require.ErrorIs(t, err, ErrInvalidArgument, "partial deletes need a limit")
	_, err = c.DeleteRelationships(ctx, DeleteRequest{Filter: testFilter("document")}, WithDeletePartial(PartialDisallowed))
	require.NoError(t, err)
	last := srv.deletes[len(srv.deletes)-1]
	require.False(t, last.GetOptionalAllowPartialDeletions())
	require.Zero(t, last.GetOptionalLimit(), "zero uses the server default")

	srv.deletePages = []*v1.DeleteRelationshipsResponse{
		{DeletedAt: &v1.ZedToken{Token: "1"}, RelationshipsDeletedCount: 2, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_PARTIAL, AfterResultCursor: &v1.Cursor{Token: "k1"}},
		{DeletedAt: &v1.ZedToken{Token: "2"}, RelationshipsDeletedCount: 1, DeletionProgress: v1.DeleteRelationshipsResponse_DELETION_PROGRESS_COMPLETE},
	}
	var progress []DeleteResult
	result, err := c.DeleteAllRelationshipsNonAtomic(ctx, DeleteAllRequest{Filter: testFilter("document")}, WithDeleteAllProgress(func(r DeleteResult) { progress = append(progress, r) }))
	require.NoError(t, err)
	require.EqualValues(t, 3, result.DeletedCount)
	require.Len(t, progress, 2)
	require.EqualValues(t, 2, progress[0].DeletedCount)
	require.Equal(t, "k1", progress[0].Cursor)
	require.EqualValues(t, 3, progress[1].DeletedCount)
	require.True(t, progress[1].Complete)
}

func TestCallOptionsReachEveryOperation(t *testing.T) {
	// WithCallOptions instantiates for every operation's option type.
	called := 0
	interceptor := grpc.OnFinish(func(error) { called++ })
	srv := &adoptionServer{}
	c := newAdoptionClient(t, srv)
	_, err := c.ReadSchema(context.Background(), ReadSchemaRequest{}, WithCallOptions[ReadSchemaOption](interceptor))
	require.NoError(t, err)
	_, err = c.DeleteRelationships(context.Background(), DeleteRequest{Filter: testFilter("document"), Limit: 1}, WithCallOptions[DeleteOption](interceptor))
	require.NoError(t, err)
	require.Equal(t, 2, called)
	require.False(t, errors.Is(err, ErrInvalidArgument))
}
