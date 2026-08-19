// Package client provides the SpiceDB client and all operations.
package client

import (
	"fmt"

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

	proto *proto.Client
}

// Option configures the client when using NewWithOpts.
type Option func(*options)

type options struct {
	insecure            bool
	allowInsecureRemote bool
	dialOptions         []grpc.DialOption
}

// WithInsecure disables TLS. Use only for testing.
//
// By itself this only permits a plaintext connection to a loopback endpoint
// (localhost, 127.0.0.0/8, ::1, or a unix socket). See root DESIGN.md,
// "RULE: Credentials over insecure transport require an explicit opt-in":
// NewWithOpts refuses to send the bearer token to any other endpoint unless
// WithInsecureAllowRemoteHost is also passed.
func WithInsecure() Option {
	return func(o *options) { o.insecure = true }
}

// WithInsecureAllowRemoteHost permits an insecure (plaintext) connection to
// send its bearer token to a non-loopback host. Named and separate from
// WithInsecure on purpose: root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in" requires an option a
// reader cannot mistake for a default, not a boolean that does double duty
// as the plaintext-transport switch. Passing this alongside WithInsecure is
// the caller stating, on purpose, "yes, send a long-lived SpiceDB token in
// cleartext to a remote host" -- do that only if you understand a SpiceDB
// token is a complete authorization bypass in anyone else's hands.
func WithInsecureAllowRemoteHost() Option {
	return func(o *options) { o.allowInsecureRemote = true }
}

// WithDialOptions adds custom gRPC dial options.
//
// Security note (root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in"): the guard NewWithOpts applies recognizes
// WithInsecure/WithInsecureAllowRemoteHost by the options themselves, and
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
	return func(o *options) { o.dialOptions = append(o.dialOptions, opts...) }
}

// NewPlaintext creates a client with an insecure (plaintext) connection.
// Use this for testing only — the lack of TLS is made obvious by the name.
//
// This only works against a loopback endpoint (localhost, 127.0.0.0/8,
// ::1, or a unix socket) -- see root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in". For a non-loopback
// endpoint, use NewWithOpts with both WithInsecure and
// WithInsecureAllowRemoteHost.
func NewPlaintext(endpoint, presharedKey string) (*Client, error) {
	return NewWithOpts(endpoint, presharedKey, WithInsecure())
}

// NewSystemTLS creates a client using the system's TLS certificate pool.
// Use this for production connections.
func NewSystemTLS(endpoint, presharedKey string) (*Client, error) {
	return NewWithOpts(endpoint, presharedKey)
}

// NewWithOpts creates a client with functional options.
func NewWithOpts(endpoint, presharedKey string, opts ...Option) (*Client, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	var protoOpts []proto.Option
	if o.insecure {
		protoOpts = append(protoOpts, proto.WithInsecure())
	}
	if o.allowInsecureRemote {
		protoOpts = append(protoOpts, proto.WithInsecureAllowRemoteHost())
	}
	if len(o.dialOptions) > 0 {
		protoOpts = append(protoOpts, proto.WithDialOptions(o.dialOptions...))
	}

	pc, err := proto.NewClient(endpoint, presharedKey, protoOpts...)
	if err != nil {
		return nil, fmt.Errorf("spicedb: failed to create client: %w", err)
	}

	return &Client{
		psc:   pc.PermissionsServiceClient,
		ssc:   pc.SchemaServiceClient,
		wsc:   pc.WatchServiceClient,
		esc:   pc.ExperimentalServiceClient,
		proto: pc,
	}, nil
}

// RawProto is the escape hatch: the underlying proto client, holding the four
// generated service clients (PermissionsServiceClient, SchemaServiceClient,
// WatchServiceClient, ExperimentalServiceClient) this Client makes its own
// calls through.
//
// Clearly-marked secondary API. Root DESIGN.md's "What NOT To Do" keeps
// channels, stubs and metadata out of the primary surface and permits exactly
// this -- "escape hatches for advanced use are acceptable as clearly marked
// secondary API" -- so that a request the idiomatic methods cannot express (an
// RPC or proto field not wrapped here, such as
// WriteRelationshipsRequest.OptionalTransactionMetadata, or the single-check
// CheckPermission RPC that CheckOne deliberately routes around) has a
// workaround short of forking the client:
//
//	resp, err := c.RawProto().PermissionsServiceClient.CheckPermission(ctx, req)
//
// Prefer this over building a second proto client alongside: the returned
// client dials nothing new. It is this Client's own connection, configured
// exactly as you configured it (including anything passed to WithDialOptions)
// and carrying the same bearer credentials, so a raw call cannot silently end
// up on a different transport than the idiomatic ones.
//
// Four things to know before reaching for it:
//
//   - The bearer token comes free. The connection carries this library's
//     PerRPCCredentials, so a raw call is authenticated exactly as an
//     idiomatic one is.
//   - A raw call is a raw call: no *client.Error mapping (you handle
//     google.golang.org/grpc/status yourself), no retry, and no deadline of
//     this library's -- set one on ctx.
//   - Do not Close the returned client. It holds this Client's connection, and
//     (*Client).Close is what releases it.
//   - No stability promise beyond grpc-go's and the generated code's.
//
// It is an accessor, never a constructor: it takes no endpoint, token, or
// transport setting and hands back a client that already exists, so it cannot
// become a second construction path around the guard in NewWithOpts -- root
// DESIGN.md, "RULE: Credentials over insecure transport require an explicit
// opt-in".
//
// Returns nil for a zero-value Client (one no constructor produced).
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
