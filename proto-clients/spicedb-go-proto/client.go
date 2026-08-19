package spicedbgoproto

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"unicode"

	v1 "github.com/authzed/spicedb-clients/proto-clients/spicedb-go-proto/gen/authzed/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
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
//
// Security note (root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in"): NewClient's guard evaluates the endpoint and
// the named WithInsecure/WithInsecureAllowRemoteHost options. It cannot see
// what an arbitrary grpc.DialOption does, and caller options are appended last
// (see the grpc.NewClient call below), so later ones win: a
// grpc.WithTransportCredentials supplied here replaces the credentials this
// package selected.
//
// It still fails closed rather than leaking. The bearer token is carried by
// bearerToken, a grpc.PerRPCCredentials whose RequireTransportSecurity returns
// !insecure, so downgrading the transport through this option without also
// passing WithInsecure makes grpc-go refuse to attach the token instead of
// sending it in cleartext. What such an option does to the connection is the
// caller's responsibility regardless.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(cfg *clientConfig) {
		cfg.dialOptions = append(cfg.dialOptions, opts...)
	}
}

// isLoopbackEndpoint reports whether the connection NewClient would actually
// open for endpoint terminates on a loopback destination: the literal
// hostname "localhost", an IP in 127.0.0.0/8, the IPv6 loopback ::1, or a
// unix domain socket target (unix:path or unix:///path). A unix socket never
// leaves the host's kernel, so it is loopback for the purposes of this check
// even though it has no IP at all.
//
// That wording is deliberate. This does not answer "does this string look
// like it names a loopback host"; it answers "will the transport dial
// loopback". Those are the same question only if this function and grpc-go
// agree on where the host ends and the rest of the target begins -- and a
// hand-rolled split of a target string will disagree with grpc-go's URI
// parse somewhere. The equivalent guard in this repo's C# and Rust clients
// did exactly that: given "127.0.0.1:443@evil.com" a last-colon split
// yields host "127.0.0.1" and reports loopback, while their transports
// parsed the same string as a URI, read "127.0.0.1:443" as userinfo, and
// connected to evil.com. Go's own transport happens not to be fooled by
// that particular input (grpc-go's DNS resolver keeps host "127.0.0.1" and
// fails on the unparseable port), but relying on that is relying on an
// accident of one input.
//
// A gRPC target has TWO places a host can hide, and BOTH are judged here:
//
//   - the endpoint (the URI path), which is what gets resolved and dialed;
//     and
//   - the authority (the URI host), which for the dns scheme is the
//     NAMESERVER grpc-go queries -- see internal/resolver/dns, which hands
//     target.URL.Host to newNetResolver and builds a net.Resolver dialing
//     it on port 53.
//
// Judging only the endpoint is not enough, and an earlier version of this
// function made exactly that mistake: "dns://evil.com/localhost:50051" has
// the loopback endpoint "localhost:50051", but every lookup for it --
// including the _grpc_config TXT query whose service config grpc-go then
// APPLIES -- goes to evil.com. Whether the returned address is honoured
// depends on host-resolver ordering (files-first on darwin, but an
// "nsswitch.conf" ordering "dns files", or an /etc/hosts without localhost,
// puts the address -- and the cleartext connection carrying the bearer
// token -- in the attacker's gift). So a target counts as loopback only
// when the endpoint is loopback AND the target carries no authority at all.
// Not even a loopback authority is accepted -- see the comment at that
// check for why a nameserver on loopback is NOT the same trust position as
// the system resolver.
//
// The target is resolved the way grpc.NewClient resolves it -- parse as a
// URI, and if the scheme names no registered resolver, re-parse as
// "<default scheme>:///" + target, as
// ClientConn.initParsedTargetAndResolverBuilder does. Two deliberate
// divergences from it remain, both of which fail closed rather than open:
//
//   - grpc.NewClient consults cc.getResolver, which checks per-dial
//     resolvers registered via grpc.WithResolvers(...) (reachable here
//     through WithDialOptions) before falling back to the global
//     resolver.Get used below. A target naming such a private scheme is
//     therefore not recognized here, takes the default-scheme fallback, and
//     lands in the endpoint with its "://" intact -- which the
//     reserved-character check refuses.
//   - resolver.GetDefaultScheme() is "passthrough", while grpc.NewClient's
//     own default is dopts.defaultScheme ("dns", or "passthrough" when a
//     custom dialer is set). That distinction cannot change this answer:
//     "dns:///" + target and "passthrough:///" + target parse to the same
//     empty authority and the same Endpoint().
//
// Each host is then taken with the same net.SplitHostPort grpc-go's DNS
// resolver and net.Dial themselves use.
//
// Anything that could move the authority under URI parsing -- userinfo, a
// query, a fragment, or a leftover '@', '/', '?', '#' or whitespace in the
// endpoint itself -- is refused before any host is considered. A
// legitimate SpiceDB target contains none of those, and failing closed on a
// weird endpoint is the correct trade for a credential leak.
//
// This is the exemption in root DESIGN.md, "RULE: Credentials over
// insecure transport require an explicit opt-in": loopback is the reason
// WithInsecure exists (local development, docker-compose, CI), so it must
// keep working with no extra ceremony. Anything else requires
// WithInsecureAllowRemoteHost -- see NewClient.
func isLoopbackEndpoint(endpoint string) bool {
	parsed, ok := parseGRPCTarget(endpoint)
	if !ok {
		return false
	}

	// url.Parse lower-cases the scheme, so this recognizes "unix:", "UNIX:",
	// "unix://" and "UNIX://" alike -- exactly the set resolver.Get("unix")
	// routes to grpc-go's unix resolver. A unix target carries a filesystem
	// path, so it legitimately holds the '/' the reserved-character check
	// below refuses, and it never leaves the host's kernel regardless of what
	// the path says. "unix-abstract" is included because grpc-go registers
	// both schemes against that same resolver and an abstract socket is
	// equally confined to the kernel. The authority must still be empty:
	// grpc-go's unix resolver rejects a non-empty host outright ("invalid
	// (non-empty) authority"), so "unix://somewhere/path" dials nothing.
	if parsed.URL.Scheme == "unix" || parsed.URL.Scheme == "unix-abstract" {
		return parsed.URL.Host == ""
	}

	// Userinfo/query/fragment in the target proper, then the same characters
	// surviving into the endpoint (e.g. "dns:///127.0.0.1:443@evil.com",
	// where the '@' lands in the path rather than in a URL authority).
	if parsed.URL.User != nil || parsed.URL.RawQuery != "" || parsed.URL.Fragment != "" {
		return false
	}

	// Any authority at all disqualifies the target. For the dns scheme the
	// authority is the nameserver every lookup for the endpoint below is sent
	// to, and the endpoint string naming it is attacker-supplied -- which is
	// the whole thing this guard exists to defend against.
	//
	// A loopback authority is NOT an exception, deliberately. The tempting
	// argument -- that a nameserver on loopback is the same trust position as
	// the system resolver, so "dns://127.0.0.1:9999/localhost:50051" is no
	// worse than "localhost:50051" -- is wrong: redirecting the system
	// resolver means editing /etc/hosts or resolv.conf, which needs root,
	// while binding a high UDP port on loopback needs no privilege at all.
	// Those are separated by the entire privilege boundary. On a shared host
	// or a multi-process container, any unprivileged local process can stand
	// up a nameserver, answer the _grpc_config TXT query with a service config
	// grpc-go will APPLY, and answer the A/AAAA lookup with an address of its
	// choosing -- over which the bearer token then travels in cleartext.
	//
	// The cost of refusing is close to nothing: an ordinary endpoint
	// ("localhost:50051") carries no authority, and the authority-form
	// "dns:///localhost:50051" has an empty one and keeps working. Only the
	// rare, deliberate resolver-directing form changes, and a caller doing
	// that on purpose can pass WithInsecureAllowRemoteHost.
	if parsed.URL.Host != "" {
		return false
	}

	target := parsed.Endpoint()
	if strings.ContainsAny(target, "@/?#") ||
		strings.IndexFunc(target, unicode.IsSpace) >= 0 {
		return false
	}
	return isLoopbackHostPort(target)
}

// isLoopbackHostPort reports whether a "host", "host:port", "[host]:port" or
// bare "[host]" string names a loopback host. Never performs a DNS lookup:
// net.ParseIP is pure parsing, so a real remote hostname is simply not an IP
// and is treated as non-loopback.
//
// The host is extracted with the same three-step sequence grpc-go's DNS
// resolver uses (internal/resolver/dns, parseTarget): a bare IP literal, then
// net.SplitHostPort, then the same split with a default port appended so a
// bare "[::1]" is de-bracketed by the parser rather than by hand.
//
// It does NOT trim brackets itself. strings.Trim(host, "[]") -- what this used
// to do -- removes any number of brackets from either end, so "]127.0.0.1[",
// "[::1", "::1]" and "[127.0.0.1]" all reported loopback. None of those was
// exploitable, because net.SplitHostPort rejects them and so nothing could be
// dialed, but hand-rolled string surgery sitting next to a parser is precisely
// the pattern that produced the bypass this guard exists to close.
//
// An empty host (from ":50051") is treated as non-loopback here, whereas
// grpc-go maps it to "localhost". That is a deliberate over-refusal: erring
// closed costs a caller nothing but an explicit opt-in.
func isLoopbackHostPort(hostPort string) bool {
	var host string
	switch {
	case net.ParseIP(hostPort) != nil:
		// A bare IPv4 or IPv6 literal, unbracketed.
		host = hostPort
	default:
		h, _, err := net.SplitHostPort(hostPort)
		if err != nil {
			h, _, err = net.SplitHostPort(hostPort + ":" + defaultDNSPort)
		}
		if err != nil {
			return false
		}
		host = h
	}

	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// defaultDNSPort is appended only to make net.SplitHostPort accept a
// port-less host, mirroring grpc-go's DNS resolver. Its value never reaches a
// connection and is irrelevant to the loopback decision.
const defaultDNSPort = "443"

// parseGRPCTarget reproduces grpc.NewClient's target resolution (see
// ClientConn.initParsedTargetAndResolverBuilder in google.golang.org/grpc):
// parse the target as a URI and keep it if its scheme names a registered
// resolver, otherwise re-parse it under the default scheme in authority
// form. See isLoopbackEndpoint above for the two divergences from grpc-go
// that remain, and why each fails closed. Reported as not-ok when grpc-go
// itself would fail to parse, which is a target it could never dial.
func parseGRPCTarget(target string) (resolver.Target, bool) {
	if u, err := url.Parse(target); err == nil && resolver.Get(u.Scheme) != nil {
		return resolver.Target{URL: *u}, true
	}
	u, err := url.Parse(resolver.GetDefaultScheme() + ":///" + target)
	if err != nil {
		return resolver.Target{}, false
	}
	return resolver.Target{URL: *u}, true
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
