package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// ReadyOption configures Ready.
type ReadyOption func(*operationOptions)

// WithReadyTimeout bounds how long Ready waits for the endpoint (the client
// default deadline, 30 seconds unless changed, otherwise).
func WithReadyTimeout(d time.Duration) ReadyOption {
	return func(o *operationOptions) { timeoutOption(o, d) }
}

// Ready waits until the endpoint accepts connections and reports it is
// serving, so a wrong or unreachable endpoint can fail at startup rather than
// on the first real call. Constructors connect lazily; Ready is the startup
// readiness check.
//
// It connects and asks the gRPC health service, waiting through connection
// failures and a not-yet-serving server, until the client default deadline,
// WithReadyTimeout or ctx ends. A server that answers but does not serve the
// health service, or rejects the call's credentials, is reachable and counts
// as ready; the credentials are checked by the first real call.
//
// When the endpoint stays unreachable, the error is CodeDeadlineExceeded and
// its message names the endpoint and the latest connection error (refused,
// DNS, TLS). A server that fails the check with Unavailable reports
// CodeUnavailable once the wait ends. Cancelling ctx returns CodeCanceled.
func (c *Client) Ready(ctx context.Context, opts ...ReadyOption) error {
	o := operationOptions{}
	applyOptions(c, &o, opts)
	ctx, cancel, e := c.operation(ctx, o, false)
	if e != nil {
		return e
	}
	defer cancel()
	conn := c.proto.Connection()
	if conn == nil {
		return invalid("client has no connection")
	}
	endpoint := c.endpoint
	if endpoint == "" {
		endpoint = "supplied connection"
	}
	health := healthpb.NewHealthClient(conn)
	callOpts := append([]grpc.CallOption{grpc.WaitForReady(true)}, o.callOptions...)
	var last error
	for attempt := 0; ; attempt++ {
		resp, err := health.Check(ctx, &healthpb.HealthCheckRequest{}, callOpts...)
		switch {
		case err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING:
			return nil
		case err == nil:
			last = fmt.Errorf("health status %s", resp.GetStatus())
		default:
			switch status.Code(err) {
			case codes.Unimplemented, codes.NotFound, codes.Unauthenticated, codes.PermissionDenied:
				// The server answered: it is reachable.
				return nil
			case codes.DeadlineExceeded, codes.Canceled:
				return readyError(ctx, endpoint, err, last)
			}
			last = err
		}
		timer := time.NewTimer(min(100*time.Millisecond<<min(attempt, 4), time.Second))
		select {
		case <-ctx.Done():
			timer.Stop()
			return readyError(ctx, endpoint, ctx.Err(), last)
		case <-timer.C:
		}
	}
}

// readyError reports why Ready gave up: cancellation as CodeCanceled, an
// endpoint that answered Unavailable as CodeUnavailable, and anything else as
// CodeDeadlineExceeded, naming the endpoint and the latest cause.
func readyError(ctx context.Context, endpoint string, err, last error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return mapGRPCError("ready", ctx.Err())
	}
	if status.Code(last) == codes.Unavailable {
		mapped := mapGRPCError("ready", last).(*Error)
		mapped.Message = fmt.Sprintf("spicedb: ready: endpoint %q unavailable: %s", endpoint, status.Convert(last).Message())
		return mapped
	}
	cause := status.Convert(err).Message()
	if last != nil {
		cause = fmt.Sprintf("%s; last result: %s", cause, last)
	}
	return &Error{Code: CodeDeadlineExceeded, Message: fmt.Sprintf("spicedb: ready: endpoint %q not ready: %s", endpoint, cause), err: errors.Join(context.DeadlineExceeded, err)}
}
