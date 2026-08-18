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
	insecure    bool
	dialOptions []grpc.DialOption
}

// WithInsecure disables TLS. Use only for testing.
func WithInsecure() Option {
	return func(o *options) { o.insecure = true }
}

// WithDialOptions adds custom gRPC dial options.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(o *options) { o.dialOptions = append(o.dialOptions, opts...) }
}

// NewPlaintext creates a client with an insecure (plaintext) connection.
// Use this for testing only — the lack of TLS is made obvious by the name.
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
