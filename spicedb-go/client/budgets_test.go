package client

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnaryRetriesShareOneDeadlineAndStopAtFourAttempts(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.Aborted, codes.ResourceExhausted} {
		t.Run(code.String(), func(t *testing.T) {
			calls := 0
			var deadline time.Time
			c := checkClient(t, &checkServer{single: func(ctx context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
				calls++
				d, ok := ctx.Deadline()
				require.True(t, ok)
				if calls == 1 {
					deadline = d
				} else {
					// gRPC reconstructs server deadlines from relative timeouts. Allow
					// scheduling jitter, while the handler delay makes a reset detectable.
					assert.WithinDuration(t, deadline, d, 50*time.Millisecond)
				}
				time.Sleep(100 * time.Millisecond)
				return nil, status.Error(code, "try again")
			}})
			_, e := c.Check(context.Background(), testRequest("a"))
			require.Error(t, e)
			if code == codes.ResourceExhausted {
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 4, calls)
			}
		})
	}
}
func TestCancellationInterruptsRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c := checkClient(t, &checkServer{single: func(_ context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
		calls++
		cancel()
		return nil, status.Error(codes.Unavailable, "retry")
	}})
	start := time.Now()
	_, e := c.Check(ctx, testRequest("a"))
	require.ErrorIs(t, e, context.Canceled)
	require.ErrorIs(t, e, ErrCanceled)
	require.Equal(t, 1, calls)
	require.Less(t, time.Since(start), time.Second)
}
func TestBulkChunksShareTotalTimeoutAndSnapshotAllInput(t *testing.T) {
	calls := 0
	requests := make([]PermissionCheck, 1001)
	for i := range requests {
		requests[i] = testRequest(fmt.Sprint(i))
		requests[i].Context = map[string]any{"nested": map[string]any{"value": "original"}}
	}
	var firstDeadline time.Time
	c := checkClient(t, &checkServer{bulk: func(ctx context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		calls++
		d, _ := ctx.Deadline()
		if calls == 1 {
			firstDeadline = d
			requests[1000].Context["nested"].(map[string]any)["value"] = "changed"
			time.Sleep(30 * time.Millisecond)
		} else {
			require.WithinDuration(t, firstDeadline, d, 10*time.Millisecond)
			require.Equal(t, "original", r.Items[0].Context.AsMap()["nested"].(map[string]any)["value"])
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return successfulBulk(r), nil
	}})
	start := time.Now()
	_, e := c.CheckBulk(context.Background(), requests, WithCheckTimeout(time.Second))
	require.ErrorIs(t, e, context.DeadlineExceeded)
	require.ErrorIs(t, e, ErrDeadlineExceeded)
	require.Equal(t, 2, calls)
	require.Less(t, time.Since(start), 1500*time.Millisecond)
}
func TestCheckIterHasNoDefaultDeadlineAndExplicitTimeoutStartsAtConsumption(t *testing.T) {
	c := checkClient(t, &checkServer{bulk: func(ctx context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		_, ok := ctx.Deadline()
		require.False(t, ok)
		return successfulBulk(r), nil
	}})
	for _, e := range c.CheckIter(context.Background(), slices.Values([]PermissionCheck{testRequest("a")})) {
		require.NoError(t, e)
	}
	c2 := checkClient(t, &checkServer{bulk: func(ctx context.Context, r *v1.CheckBulkPermissionsRequest) (*v1.CheckBulkPermissionsResponse, error) {
		d, ok := ctx.Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(d), 20*time.Millisecond)
		return successfulBulk(r), nil
	}})
	seq := c2.CheckIter(context.Background(), slices.Values([]PermissionCheck{testRequest("a")}), WithCheckTimeout(80*time.Millisecond))
	time.Sleep(100 * time.Millisecond)
	for _, e := range seq {
		require.NoError(t, e)
	}
}
func TestCallerDeadlineWinsAndPreCanceledCallsDoNoWork(t *testing.T) {
	c := &Client{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := c.Check(ctx, testRequest("a"))
	require.ErrorIs(t, e, context.Canceled)
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, e = c.Check(ctx, testRequest("a"), WithCheckTimeout(time.Minute))
	require.ErrorIs(t, e, context.DeadlineExceeded)
}

func TestNativeCancellationDuringSingularRPC(t *testing.T) {
	started := make(chan struct{})
	c := checkClient(t, &checkServer{single: func(ctx context.Context, _ *v1.CheckPermissionRequest) (*v1.CheckPermissionResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Check(ctx, testRequest("a")); done <- e }()
	<-started
	cancel()
	select {
	case e := <-done:
		require.ErrorIs(t, e, context.Canceled)
		require.ErrorIs(t, e, ErrCanceled)
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt singular RPC")
	}
}
