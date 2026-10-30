// Package client provides the SpiceDB client and all operations.
package client

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	proto "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/grpc"
)

// Client is the idiomatic SpiceDB client. Use NewPlaintext, NewSystemTLS, or
// NewWithOpts to create one. Call Close when done with it to release the
// underlying gRPC connection deterministically -- see root DESIGN.md, "RULE:
// Abandoning a stream must release it".
type Client struct {
	psc v1.PermissionsServiceClient
	ssc v1.SchemaServiceClient
	wsc v1.WatchServiceClient
	esc v1.ExperimentalServiceClient

	proto             *proto.Client
	endpoint          string // as given to the constructor; "" for NewFromConn
	defaultTimeout    time.Duration
	noDefaultDeadline bool
	retries           *RetryPolicy // nil means DefaultRetryPolicy
	noStreamRetries   bool
	opRetry           map[reflect.Type]*RetryPolicy
}

// Option configures the client when using NewWithOpts.
type Option func(*options)

type options struct {
	insecure            bool
	allowInsecureRemote bool
	dialOptions         []grpc.DialOption
	defaultTimeout      time.Duration
	err                 error

	// transportOptionSet records options that only apply when this client
	// dials, so NewFromConn can reject them.
	transportOptionSet bool

	noDefaultDeadline     bool
	retryPolicy           *RetryPolicy
	retriesDisabled       bool
	streamRetriesDisabled bool
	opRetry               map[reflect.Type]*RetryPolicy
}

// build applies options and returns the client-wide behavior they select.
func buildOptions(opts []Option) (*options, error) {
	o := &options{defaultTimeout: 30 * time.Second}
	for _, opt := range opts {
		opt(o)
	}
	return o, o.err
}

func (o *options) apply(c *Client) {
	c.defaultTimeout = o.defaultTimeout
	c.noDefaultDeadline = o.noDefaultDeadline
	c.noStreamRetries = o.streamRetriesDisabled
	c.opRetry = o.opRetry
	switch {
	case o.retriesDisabled:
		c.retries = &RetryPolicy{}
	case o.retryPolicy != nil:
		c.retries = o.retryPolicy
	}
}

// WithInsecure selects plaintext transport. Remote credentials also require
// WithRemotePlaintextCredentials(RemotePlaintextAllowed).
func WithInsecure() Option {
	return func(o *options) { o.insecure, o.transportOptionSet = true, true }
}

// RemotePlaintextCredentials controls sending credentials to remote plaintext endpoints.
type RemotePlaintextCredentials uint8

const (
	RemotePlaintextRejected RemotePlaintextCredentials = iota
	RemotePlaintextAllowed
)

// WithRemotePlaintextCredentials explicitly permits or rejects credentials over
// remote plaintext. The default rejects them. This option does not disable TLS.
func WithRemotePlaintextCredentials(policy RemotePlaintextCredentials) Option {
	return func(o *options) {
		if policy != RemotePlaintextRejected && policy != RemotePlaintextAllowed {
			o.err = invalid("unknown remote plaintext credential policy")
			return
		}
		o.allowInsecureRemote = policy == RemotePlaintextAllowed
		o.transportOptionSet = true
	}
}

// WithDialOptions adds custom gRPC dial options.
//
// Security note (root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in"): the guard NewWithOpts applies recognizes
// WithInsecure/WithRemotePlaintextCredentials by the options themselves, and
// cannot see what an arbitrary grpc.DialOption does to the connection.
// Caller options are appended AFTER this library's own, and later dial options
// win in grpc-go, so a grpc.WithTransportCredentials passed here replaces the
// transport security this library selected -- the guard evaluates the endpoint
// and the named options, not the result.
//
// This is not a credential leak today, and the reason is worth knowing rather
// than assuming: the bearer credentials are a grpc.PerRPCCredentials whose
// RequireTransportSecurity reports !insecure, so downgrading the transport
// here while leaving WithInsecure unset makes grpc-go refuse to send the token
// at all rather than send it in cleartext. It fails closed. Everything this
// option does to the connection is nonetheless the caller's responsibility.
func WithDialOptions(opts ...grpc.DialOption) Option {
	opts = append([]grpc.DialOption(nil), opts...)
	return func(o *options) {
		o.dialOptions = append(o.dialOptions, opts...)
		o.transportOptionSet = true
	}
}

// NewPlaintext creates a plaintext client. The endpoint is host:port or
// http://host:port; the port is required (a bare "localhost" is refused). Remote endpoints require
// WithRemotePlaintextCredentials(RemotePlaintextAllowed) to send credentials.
func NewPlaintext(endpoint, presharedKey string, opts ...Option) (*Client, error) {
	return NewWithOpts(endpoint, presharedKey, append([]Option{WithInsecure()}, opts...)...)
}

// NewSystemTLS creates a client using the system's TLS certificate pool.
// Use this for production connections. The endpoint is host[:port] or
// https://host[:port] (443 when the port is omitted).
func NewSystemTLS(endpoint, presharedKey string, opts ...Option) (*Client, error) {
	return NewWithOpts(endpoint, presharedKey, opts...)
}

// NewWithOpts creates a client with functional options.
//
// The endpoint is host:port, http://host:port (plaintext, with WithInsecure),
// https://host[:port] (TLS; 443 when the port is omitted), or a gRPC target:
// dns:///host:port (client-side DNS resolution), unix:/path/to.sock or
// unix:///path/to.sock (a local socket, loopback for the plaintext check), or
// passthrough:///host:port. A URL whose scheme disagrees with the transport, a
// plaintext endpoint without a port, any other scheme (including other gRPC
// resolvers), or an endpoint with a path, query, fragment, userinfo or
// non-numeric port fails here with ErrInvalidArgument naming the problem.
//
// Construction does not connect. Call Ready at startup to fail on a wrong or
// unreachable endpoint before the first real call.
func NewWithOpts(endpoint, presharedKey string, opts ...Option) (*Client, error) {
	// An empty or whitespace-only key (usually a blank environment variable)
	// would otherwise be sent as a bare "Bearer" header the server rejects on
	// the first call. See root DESIGN.md, "Reject unusable credentials and
	// trust material at construction".
	if strings.TrimSpace(presharedKey) == "" {
		return nil, fmt.Errorf("spicedb: failed to create client: preshared key must not be empty or whitespace: %w", ErrInvalidArgument)
	}
	o, err := buildOptions(opts)
	if err != nil {
		return nil, err
	}
	var protoOpts []proto.Option
	if o.insecure {
		protoOpts = append(protoOpts, proto.WithInsecure())
	}
	if o.allowInsecureRemote {
		protoOpts = append(protoOpts, proto.WithInsecureAllowRemoteHost())
	}
	protoOpts = append(protoOpts, proto.WithDialOptions(append(o.dialOptions, grpc.WithDisableRetry(), grpc.WithDisableServiceConfig())...))

	pc, err := proto.NewClient(endpoint, presharedKey, protoOpts...)
	if err != nil {
		// The insecure-remote-host refusal is a caller argument this client
		// rejects before any connection exists, so it surfaces as
		// ErrInvalidArgument -- the same sentinel a filter the wire cannot
		// express uses. The proto tier's own error type is an implementation
		// detail a caller of this package should never have to know. See root
		// DESIGN.md, "RULE: Credentials over insecure transport require an
		// explicit opt-in", clause 4.
		// An endpoint in a form this client cannot dial is likewise refused
		// at construction rather than producing a client whose every call
		// fails.
		if errors.Is(err, proto.ErrInsecureRemoteHost) {
			// Name this package's opt-in, not the proto tier's option.
			return nil, fmt.Errorf(
				"spicedb: failed to create client: refusing to send credentials over plaintext to non-loopback endpoint %q: use TLS (NewSystemTLS), or allow remote plaintext explicitly with NewPlaintext(endpoint, key, WithRemotePlaintextCredentials(RemotePlaintextAllowed)) (with NewWithOpts: WithInsecure() and WithRemotePlaintextCredentials(RemotePlaintextAllowed)): %w: %w",
				endpoint, proto.ErrInsecureRemoteHost, ErrInvalidArgument)
		}
		if errors.Is(err, proto.ErrInvalidEndpoint) {
			return nil, fmt.Errorf("spicedb: failed to create client: %w: %w", err, ErrInvalidArgument)
		}
		return nil, fmt.Errorf("spicedb: failed to create client: %w", err)
	}

	c := newClient(pc, o)
	c.endpoint = endpoint
	return c, nil
}

func newClient(pc *proto.Client, o *options) *Client {
	c := &Client{
		psc:   pc.PermissionsServiceClient,
		ssc:   pc.SchemaServiceClient,
		wsc:   pc.WatchServiceClient,
		esc:   pc.ExperimentalServiceClient,
		proto: pc,
	}
	o.apply(c)
	return c
}

// NewFromConn creates a client over an existing connection, such as an
// in-memory or buffered-listener connection, or one from SpiceDB's test
// server. The connection stays caller-owned: Close does not close it.
//
// The connection must already carry its credentials and transport security,
// so WithInsecure, WithRemotePlaintextCredentials and WithDialOptions are
// rejected. Operation behavior (WithDefaultTimeout, WithDefaultDeadline,
// WithRetries, WithRetryPolicy, WithStreamRetries) applies as usual. gRPC's
// own retries are not disabled on a supplied connection; configure the
// connection without them so mutations are never replayed.
func NewFromConn(conn grpc.ClientConnInterface, opts ...Option) (*Client, error) {
	if conn == nil {
		return nil, invalid("connection is required")
	}
	o, err := buildOptions(opts)
	if err != nil {
		return nil, err
	}
	if o.transportOptionSet {
		return nil, invalid("transport and credential options do not apply to a supplied connection")
	}
	return newClient(proto.NewClientFromConn(conn), o), nil
}

// RawProto is the explicitly secondary accessor for the existing authenticated connection.
// It creates no connection and transfers no ownership. Do not close it independently.
// Raw calls bypass native validation, errors, budgets and application retries. The
// connection disables transport retries so mutation calls are never automatically replayed.
// WithDialOptions permits custom trust/mTLS; caller transport options override defaults,
// while native credential guards continue to apply. Returns nil for a zero-value Client.
func (c *Client) RawProto() *proto.Client {
	if c == nil {
		return nil
	}
	return c.proto
}

// Close releases the underlying gRPC connection. Idempotent -- safe to call
// more than once, including concurrently with itself (delegates to
// proto.Client.Close, which guards with a CompareAndSwap).
//
// A caller that only ever makes unary calls and never streams may not need
// this, but every streaming call on Client (ReadRelationships,
// LookupResources, LookupSubjects, Watch, ExportRelationships) shares this
// one connection, and there was previously no way to release it
// deterministically -- see root DESIGN.md, "RULE: Abandoning a stream must
// release it".
//
// A Client with no connection is a no-op to close, not a panic. proto is
// unexported, so a zero-value `client.Client{}` -- or one assembled field by
// field in a test -- has a nil proto that no constructor could produce. Close
// is the one method such a value is most likely to reach, via a `defer
// c.Close()` copied from production code, and dereferencing nil there would
// turn a test double into a crash.
func (c *Client) Close() error {
	if c == nil || c.proto == nil {
		return nil
	}
	return c.proto.Close()
}

// WithDefaultTimeout sets the total unary operation budget (30 seconds by default).
func WithDefaultTimeout(d time.Duration) Option {
	return func(o *options) {
		if d <= 0 {
			o.err = invalid("default timeout must be positive")
			return
		}
		o.defaultTimeout = d
	}
}
