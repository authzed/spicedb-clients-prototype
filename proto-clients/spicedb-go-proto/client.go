package spicedbgoproto

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps all generated gRPC service clients for SpiceDB.
type Client struct {
	PermissionsServiceClient  v1.PermissionsServiceClient
	SchemaServiceClient       v1.SchemaServiceClient
	WatchServiceClient        v1.WatchServiceClient
	ExperimentalServiceClient v1.ExperimentalServiceClient

	conn   *grpc.ClientConn
	closed atomic.Bool
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	insecure            bool
	allowInsecureRemote bool
	dialOptions         []grpc.DialOption
}

// WithInsecure disables TLS (for testing).
//
// By itself this only permits a plaintext connection to a loopback endpoint
// (localhost, 127.0.0.0/8, ::1, or a unix socket) -- see isLoopbackEndpoint
// below and root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in". NewClient refuses to send the bearer token
// to any other endpoint unless WithInsecureAllowRemoteHost is also passed.
func WithInsecure() Option {
	return func(cfg *clientConfig) {
		cfg.insecure = true
	}
}

// WithInsecureAllowRemoteHost permits an insecure (plaintext) connection to
// send its bearer token to a non-loopback host. Named and separate from
// WithInsecure on purpose: root DESIGN.md, "RULE: Credentials over insecure
// transport require an explicit opt-in" requires an option a reader cannot
// mistake for a default, not a boolean that does double duty as the
// plaintext-transport switch. Passing this alongside WithInsecure is the
// caller stating, on purpose, "yes, send a long-lived SpiceDB token in
// cleartext to a remote host" -- do that only if you understand a SpiceDB
// token is a complete authorization bypass in anyone else's hands.
func WithInsecureAllowRemoteHost() Option {
	return func(cfg *clientConfig) {
		cfg.allowInsecureRemote = true
	}
}

// WithDialOptions adds custom gRPC dial options.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(cfg *clientConfig) {
		cfg.dialOptions = append(cfg.dialOptions, opts...)
	}
}

// isLoopbackEndpoint reports whether a gRPC target string names a loopback
// destination: the literal hostname "localhost", an IP in 127.0.0.0/8, the
// IPv6 loopback ::1, or a unix domain socket target (unix:path or
// unix:///path). A unix socket never leaves the host's kernel, so it is
// loopback for the purposes of this check even though it has no IP at all.
//
// This is the exemption in root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in": loopback is the reason
// WithInsecure exists (local development, docker-compose, CI), so it must
// keep working with no extra ceremony. Anything else requires
// WithInsecureAllowRemoteHost -- see NewClient.
func isLoopbackEndpoint(endpoint string) bool {
	target := endpoint

	// Strip a grpc-go resolver scheme prefix (dns:///, passthrough:///,
	// unix://, ...) per https://github.com/grpc/grpc/blob/master/doc/naming.md.
	if idx := strings.Index(target, "://"); idx >= 0 {
		scheme, rest := target[:idx], target[idx+3:]
		if strings.EqualFold(scheme, "unix") {
			return true
		}
		// An authority-form target (e.g. "passthrough:///host:port") has a
		// third slash separating the (here, empty) authority from the
		// endpoint; strip it so SplitHostPort below sees "host:port", not
		// "/host:port".
		target = strings.TrimPrefix(rest, "/")
	} else if strings.HasPrefix(target, "unix:") {
		// grpc-go also accepts the unprefixed "unix:path" form.
		return true
	}

	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")

	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// retryServiceConfig is the gRPC JSON service config installed by default on
// every Client. See the comment on its use in NewClient for why it has two
// methodConfig entries instead of one, and why RESOURCE_EXHAUSTED is absent
// from retryableStatusCodes. Exported as a named const (rather than an
// inline string literal) so client_test.go can parse and assert on the
// exact config NewClient installs, instead of a separately maintained copy
// that could silently drift from it.
const retryServiceConfig = `{
  "methodConfig": [
    {
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
        "retryableStatusCodes": ["UNAVAILABLE", "ABORTED"]
      }
    },
    {
      "name": [
        {"service": "authzed.api.v1.PermissionsService", "method": "WriteRelationships"},
        {"service": "authzed.api.v1.PermissionsService", "method": "DeleteRelationships"},
        {"service": "authzed.api.v1.PermissionsService", "method": "ImportBulkRelationships"},
        {"service": "authzed.api.v1.SchemaService", "method": "WriteSchema"},
        {"service": "authzed.api.v1.ExperimentalService", "method": "ExperimentalRegisterRelationshipCounter"},
        {"service": "authzed.api.v1.ExperimentalService", "method": "ExperimentalUnregisterRelationshipCounter"},
        {"service": "authzed.api.v1.ExperimentalService", "method": "BulkImportRelationships"}
      ]
    }
  ]
}`

// bearerToken implements credentials.PerRPCCredentials for bearer token auth.
type bearerToken struct {
	token    string
	insecure bool
}

func (b bearerToken) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + b.token}, nil
}

// RequireTransportSecurity reports whether this credential requires
// transport security. Returning !b.insecure means grpc-go's own check --
// which normally refuses to attach PerRPCCredentials to a channel that
// isn't secure -- passes exactly when the connection IS insecure. That is
// deliberate: WithInsecure exists for local development, and grpc's
// default refusal would break it outright.
//
// What stops this from silently shipping a bearer token to an arbitrary
// insecure host is NOT here -- it's NewClient's own check, just below,
// which refuses to construct a Client at all when insecure and the
// endpoint isn't loopback and the caller hasn't passed
// WithInsecureAllowRemoteHost. See isLoopbackEndpoint above and root
// DESIGN.md, "RULE: Credentials over insecure transport require an
// explicit opt-in".
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

	// See root DESIGN.md, "RULE: Credentials over insecure transport
	// require an explicit opt-in". Refuse before any dial option, transport
	// credential, or connection is created -- so a bearer token can never
	// reach the wire for a rejected combination, not merely fail loudly
	// after the fact.
	if cfg.insecure && !cfg.allowInsecureRemote && !isLoopbackEndpoint(endpoint) {
		return nil, fmt.Errorf(
			"spicedb: refusing to send credentials over an insecure (plaintext) connection to non-loopback endpoint %q: use TLS (drop WithInsecure), or pass WithInsecureAllowRemoteHost() if you intend to send a bearer token in cleartext to a remote host",
			endpoint,
		)
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
	//
	// Two methodConfig entries, not one, per root DESIGN.md "RULE: Automatic
	// retry is for idempotent operations only":
	//
	//   - The first entry is a SERVICE-level match (no "method") for all four
	//     services, carrying the retryPolicy. This is the default for every
	//     RPC on those services, including reads.
	//   - The second entry METHOD-level-matches the seven mutation RPCs that
	//     are not safely retryable and carries no retryPolicy at all. gRPC's
	//     service-config resolution (google.golang.org/grpc/clientconn.go's
	//     getMethodConfig) always prefers an exact "/service/method" match
	//     over a "/service/" wildcard, so these seven RPCs get no retry
	//     policy -- overriding the broader entry above -- while every other
	//     RPC on the same services still retries. WriteRelationships (may
	//     carry OPERATION_CREATE or preconditions) and DeleteRelationships/
	//     WriteSchema/ImportBulkRelationships/the counter register-unregister
	//     calls (may carry preconditions, or are not idempotent to replay)
	//     are six of the seven: if one commits and the response is lost, a
	//     retry surfaces ALREADY_EXISTS/FAILED_PRECONDITION for a write that
	//     in fact succeeded.
	//
	//     The seventh, ExperimentalService.BulkImportRelationships, is the
	//     deprecated RPC ImportBulkRelationships superseded -- still present
	//     on the wire (option deprecated = true, not removed) and still
	//     reachable directly through Client.ExperimentalServiceClient, which
	//     this package exports. Deprecation is a documentation signal, not
	//     an enforcement mechanism: nothing stops a caller from invoking it,
	//     and it is exactly as non-idempotent a client-streaming bulk write
	//     as its replacement, so it needs the same exclusion. Audited every
	//     other RPC on PermissionsService, SchemaService, and
	//     ExperimentalService (including the rest of ExperimentalService's
	//     deprecated surface -- BulkExportRelationships, BulkCheckPermission,
	//     ExperimentalReflectSchema, ExperimentalComputablePermissions,
	//     ExperimentalDependentRelations, ExperimentalDiffSchema) against
	//     schema_service.proto/permission_service.proto/
	//     experimental_service.proto: every one of them is a read, so
	//     retrying them is safe and none needed adding here.
	//
	// RESOURCE_EXHAUSTED is deliberately absent from retryableStatusCodes: in
	// SpiceDB it signals memory load-shed or a deterministic
	// MaxDepthExceeded, never a transient hiccup.
	//
	// Backoff jitter: grpc-go's retry implementation (stream.go) already
	// randomizes each computed backoff by a factor of 0.8-1.2 (see gRFC A6),
	// independent of and not configurable through this JSON service config.
	// That is narrower than full jitter (uniform(0, cap)) but is built into
	// the retry mechanism itself, not something this client authors.
	dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(retryServiceConfig))

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
		conn:                      conn,
	}, nil
}

// Close releases the underlying gRPC connection. Idempotent -- safe to call
// more than once, including concurrently with itself.
//
// See root DESIGN.md, "RULE: Abandoning a stream must release it": a caller
// that has abandoned every in-flight call still holds a live transport (and
// any connection-level resources -- goroutines, sockets) until Close is
// called. grpc.ClientConn.Close itself is not documented as safe to call
// twice, so this guards with a CompareAndSwap rather than relying on that.
//
// A Client with no connection is a no-op to close, not a panic. conn is
// unexported, so a Client assembled by hand -- `&proto.Client{
// PermissionsServiceClient: stub}` in a test, or a zero value -- has a nil
// conn that NewClient could never produce. Close is the one method such a
// value is most likely to reach (via a defer written to match production
// code), and dereferencing nil there would turn a test double into a crash.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.conn.Close()
}
