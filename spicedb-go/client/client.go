// Package client provides the SpiceDB client and all operations.
package client

import (
	"fmt"

	proto "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto"
	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/grpc"
)

// Client is the idiomatic SpiceDB client. Use NewPlaintext, NewSystemTLS, or
// NewWithOpts to create one.
type Client struct {
	psc v1.PermissionsServiceClient
	ssc v1.SchemaServiceClient
	wsc v1.WatchServiceClient
	esc v1.ExperimentalServiceClient
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
		psc: pc.PermissionsServiceClient,
		ssc: pc.SchemaServiceClient,
		wsc: pc.WatchServiceClient,
		esc: pc.ExperimentalServiceClient,
	}, nil
}

// newFromProto creates a Client from a proto client. Used for testing.
func newFromProto(psc v1.PermissionsServiceClient, ssc v1.SchemaServiceClient, wsc v1.WatchServiceClient) *Client {
	return &Client{psc: psc, ssc: ssc, wsc: wsc}
}

