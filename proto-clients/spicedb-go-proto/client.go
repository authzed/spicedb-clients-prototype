package spicedbgoproto

import (
	"context"
	"fmt"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps all generated gRPC service clients for SpiceDB.
type Client struct {
	PermissionsServiceClient   v1.PermissionsServiceClient
	SchemaServiceClient        v1.SchemaServiceClient
	WatchServiceClient         v1.WatchServiceClient
	ExperimentalServiceClient  v1.ExperimentalServiceClient
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	insecure    bool
	dialOptions []grpc.DialOption
}

// WithInsecure disables TLS (for testing).
func WithInsecure() Option {
	return func(cfg *clientConfig) {
		cfg.insecure = true
	}
}

// WithDialOptions adds custom gRPC dial options.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(cfg *clientConfig) {
		cfg.dialOptions = append(cfg.dialOptions, opts...)
	}
}

// bearerToken implements credentials.PerRPCCredentials for bearer token auth.
type bearerToken struct {
	token    string
	insecure bool
}

func (b bearerToken) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

func (b bearerToken) RequireTransportSecurity() bool {
	return !b.insecure
}

// NewClient creates a new SpiceDB client connected to the given endpoint,
// authenticated with the given bearer token.
func NewClient(endpoint string, token string, opts ...Option) (*Client, error) {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var dialOpts []grpc.DialOption

	if cfg.insecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(nil)))
	}

	dialOpts = append(dialOpts, grpc.WithPerRPCCredentials(bearerToken{
		token:    token,
		insecure: cfg.insecure,
	}))

	// Retry transient gRPC errors with exponential backoff via gRPC's
	// built-in service-config retry policy. Placed before user-supplied dial
	// options so a caller-provided grpc.WithDefaultServiceConfig (via
	// WithDialOptions) takes precedence, since later dial options override
	// earlier ones in grpc-go.
	dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(`{
  "methodConfig": [{
    "name": [
      {"service": "authzed.api.v1.PermissionsService"},
      {"service": "authzed.api.v1.SchemaService"},
      {"service": "authzed.api.v1.WatchService"},
      {"service": "authzed.api.v1.ExperimentalService"}
    ],
    "retryPolicy": {
      "maxAttempts": 4,
      "initialBackoff": "0.1s",
      "maxBackoff": "5s",
      "backoffMultiplier": 2,
      "retryableStatusCodes": ["UNAVAILABLE", "RESOURCE_EXHAUSTED", "ABORTED"]
    }
  }]
}`))

	dialOpts = append(dialOpts, cfg.dialOptions...)

	conn, err := grpc.NewClient(endpoint, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	return &Client{
		PermissionsServiceClient:  v1.NewPermissionsServiceClient(conn),
		SchemaServiceClient:       v1.NewSchemaServiceClient(conn),
		WatchServiceClient:        v1.NewWatchServiceClient(conn),
		ExperimentalServiceClient: v1.NewExperimentalServiceClient(conn),
	}, nil
}
