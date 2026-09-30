# spicedb-elixir-proto -- Design Manifest

This is the Elixir proto client for SpiceDB. It wraps buf-generated gRPC
stubs with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `lib/gen/` is produced by `buf generate` and MUST NOT be
manually modified. These files are regenerated on every `mage gen`.

## Dependency Choices

- **`grpc ~> 1.0`** -- client-side gRPC transport. As of the 1.0 line, grpc-elixir
  split into a client package (`grpc`) and a server package (`grpc_server`); `grpc`
  depends on `grpc_core` (required) but never on `grpc_server` -- confirmed via
  `https://hex.pm/api/packages/grpc/releases/1.0.5`, whose `requirements` list has
  no `grpc_server` entry. This proto tier is client-only, so it depends on `grpc`
  alone, never `grpc_server`.
- **`mint ~> 1.9`** -- the adapter passed to `GRPC.Stub.connect/2`. Pure Elixir, no
  NIF/C build dependency, unlike `gun` (grpc-elixir's own default adapter), whose
  transitive deps need a C toolchain.
- **`protobuf ~> 0.17`** -- runtime support for the buf-generated code under
  `lib/gen/`. Must match the `protoc_gen_elixir_version` stamped into every
  generated module (`"0.17.0"`), since the generator and this runtime library
  ship from the same `protobuf` hex package and must agree on wire format. See
  "buf.gen.yaml: local plugin, not a BSR remote plugin" below for which
  generator produces that stamp.
- No `castore` dependency. grpc-elixir's own zero-config default (triggered when no
  `:cred` is passed to `GRPC.Stub.connect/2`) falls back to `CAStore`, a statically
  bundled Mozilla CA list -- not the OS trust store, confirmed via that package's
  hex.pm description and README. Rather than depend on that fallback, `client.ex`
  always builds its own default credential explicitly from
  `:public_key.cacerts_get/0` (the OS trust store), so `castore` is never used. See
  "Default TLS Trust Source" below.

## buf.gen.yaml: local plugin, not a BSR remote plugin

No `buf.build` remote plugin for Elixir exists -- verified against the live BSR
with `buf registry plugin info`; every guessed community plugin name came back
"does not exist". `protoc-gen-elixir` generates both message and gRPC service code
from one invocation via the `plugins=grpc` opt, unlike the other languages here
that need separate message/service remote plugins.

The `one_file_per_module=true` opt is required too, not cosmetic: without it,
protoc-gen-elixir 0.17.0 names each output file
`<underscored base_module>/<proto file path>` (confirmed by reading its
`generator.ex`), which duplicates the path whenever a `.proto` file's own
directory already mirrors its package -- true of every `authzed.api.*` file
here, e.g. `authzed/api/v1/watch_service.pb.ex` would land at
`lib/gen/authzed/api/v1/authzed/api/v1/watch_service.pb.ex`. Verified directly:
generating without the opt reproduces the doubled path; adding it produces one
file per Elixir module at the expected path.

### Generator: `github.com/TrogonStack/protoc-gen`'s Go `protoc-gen-elixir`, not the escript

The generator is `github.com/TrogonStack/protoc-gen/cmd/protoc-gen-elixir`, a Go
reimplementation of the upstream `protoc-gen-elixir` escript, aimed at
byte-compatible output with it (`elixir-protobuf/protobuf`'s own CI checks that
parity, see elixir-protobuf/protobuf#444) and supporting the same `plugins=grpc`
and `one_file_per_module` opts. It's pinned in this directory's `go.mod` via a
`tool` directive and invoked from `buf.gen.yaml` as `local: [go, tool,
protoc-gen-elixir]` -- `go tool` builds and resolves it on demand from the pinned
version, so there's no install step, no escript, and no `PATH` manipulation in
`Gen()`.

This is a pinned, reproducible Go binary that fits the Go/mage code factory this
repo is built around, unlike the escript, which needed a separate `mix
escript.install` step outside that toolchain.

The upstream release is tagged `protoc-gen-elixir@v0.1.1`, but that tag isn't a
resolvable Go module version -- `TrogonStack/protoc-gen` is a single-module repo
tagged per-command, not with a plain `vX.Y.Z` on the root module -- so `go.mod`
pins the commit that tag points to as a pseudo-version instead. See the comment
above that `require` line in `go.mod`.

Verified against the real `authzed/api` corpus: the Go generator's output is
byte-identical to the escript's for every message and enum module. The two
differ only in `*_service.pb.ex` files, and only in whitespace -- the escript
wraps a long `rpc :Name, ReqType, RespType` call across three aligned lines and
blank-lines between every rpc definition; the Go generator keeps each rpc call
on one line and only blank-lines where needed. No difference in module names,
RPC names, types, or streaming wrappers. `lib/gen/` is committed as whatever
`buf generate` deterministically produces through this pipeline -- no separate
format pass is applied or required.

## Additional Code (for Claude)

### Required Exports

Create a `lib/spicedb_proto/client.ex` file with:

1. **`SpicedbProto.Client` struct** -- holds only `:channel`. Unlike this repo's
   Ruby/Go/Java clients, a generated grpc-elixir service module takes the channel
   as an explicit argument on every call (e.g.
   `Authzed.Api.V1.PermissionsService.Stub.check_permission(client.channel,
   request)`) rather than binding to it at stub-construction time, so there is
   nothing analogous to Ruby's `Client#permissions`/`#schema`/etc. readers to
   build -- callers invoke the generated `Stub` modules directly with
   `client.channel`.

2. **`connect/3`** -- `connect(endpoint, token, opts \\ [])`, returning
   `{:ok, client}` or `{:error, reason}`:
   - Injects the bearer token via the channel-level `:headers` option passed
     directly to `GRPC.Stub.connect/2` (`headers: %{"authorization" => "Bearer
     #{token}"}`). No custom interceptor is needed: `grpc_core`'s
     `GRPC.Transport.HTTP2.client_headers_without_reserved/2` merges the
     channel's `:headers` into every request automatically. This is simpler
     than Ruby's split secure/insecure approach (call credentials vs. an
     interceptor), because grpc-elixir has one bearer-token mechanism that
     works on both transports.
   - Builds a `%GRPC.Credential{}` for the secure path (see "Default TLS
     Trust Source" below); passes no `:cred` at all for the insecure path
     (grpc-elixir's own target normalization uses the presence of `:cred` to
     pick the "https"/"http" scheme for a schemeless target).
   - `ca_cert`/`client_cert`/`client_key` are PEM strings, converted from PEM to
     the DER form Erlang's `:ssl` options expect via `:public_key.pem_decode/1`
     (verified shapes: `{:Certificate, der, :not_encrypted}` for certificates,
     `{:PrivateKeyInfo, der, :not_encrypted}` for PKCS8 private keys).

3. **`close/1`** -- closes the underlying gRPC channel.

4. **`loopback_endpoint?/1`** -- see "Loopback Guard" below.

5. **`SpicedbProto.InsecureRemoteHostError`** -- raised by `connect/3`, before any
   channel or credential is built, when `insecure: true`, the endpoint is not
   loopback, and `allow_insecure_remote_credentials` is not `true`. A distinct
   exception (not a bare `ArgumentError`) so the idiomatic client can rescue this
   refusal without also catching the TLS trust-material validation errors
   `connect/3` raises for unrelated reasons -- mirrors Ruby's
   `InsecureRemoteHostError`. See root DESIGN.md, "RULE: Credentials over insecure
   transport require an explicit opt-in", clause 4.

### Default TLS Trust Source

Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
requires the default secure path to delegate to the ecosystem's default trust
source, and requires a caller override wherever that default is not
operator-writable. grpc-elixir's own zero-config default is `CAStore` -- a
statically bundled, non-operator-writable list. Rather than inherit that
non-operator-writable default, `connect/3` builds its own default credential from
`:public_key.cacerts_get/0` (OTP 25+, delegates to the actual OS trust store --
verified to return the platform's real trust anchors on this machine), which is
operator-writable on every platform that ships a trust store. This means the
override parameters (`ca_cert`, `client_cert`, `client_key`) are optional-but-nice
here rather than required, per that RULE's own distinction -- a nicer default than
grpc-elixir ships on its own.

### Loopback Guard

Root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
opt-in", requires the guard's answer to be the transport's answer: whichever
parser the client actually dials with, not a hand-rolled split. grpc-elixir's real
fallback parser for arbitrary "host:port" targets is
`GRPC.Client.Connection.EndpointResolver`, but that module is `@moduledoc false`
-- undocumented, not part of grpc-elixir's public API, free to change in a patch
release. Depending on it directly would tie this security guard to an
implementation detail. This is the same tradeoff this repo's Ruby client carries
permanently, for the analogous reason (grpc-ruby parses targets in C-core, with no
Ruby-callable equivalent).

So `loopback_endpoint?/1` hand-rolls the split instead, refusing outright any
endpoint containing a character that could move the authority under URI parsing
(`@`, `/`, `?`, `#`, whitespace), then splitting a bracketed host, a bare IPv6
literal, or a single-colon "host:port" with a numeric port, matching grpc-ruby's
(and this Ruby client's) approach.

**Verified empirically against grpc 1.0.5** that this class of bypass does not
exist in grpc-elixir either: `GRPC.Stub.connect("127.0.0.1:443@evil.com", ...)`
raises `ArgumentError` from `:erlang.binary_to_integer("443@evil.com")` while
parsing the port, and never contacts `evil.com`. The guard above exists so this
client does not depend on that continuing to be true.

**A related, unrelated-to-security finding from the same verification pass:**
grpc-elixir 1.0.5's schemeless target handling only matches a target that splits
into exactly one or two colon-separated parts. A bracketed IPv6 literal with no
scheme prefix (e.g. `"[::1]:50051"`) therefore raises `CaseClauseError` instead of
connecting. `connect/3` works around this by prefixing `"ipv6:"` onto any endpoint
starting with `"["` before calling `GRPC.Stub.connect/2`, so this client's public
endpoint format matches every other language client here (bracketed IPv6 allowed,
no scheme prefix required from the caller).

### Tests

Use `ExUnit` for all assertions.

Create `test/spicedb_proto/client_test.exs` with:

1. **`loopback_endpoint?/1` truth table** -- loopback spellings (localhost,
   127.0.0.0/8, `::1`, `unix:` targets, case-insensitive), non-loopback hosts, and
   the authority-shifting fixture set (`127.0.0.1:443@evil.com` and its variants)
   that defeated the equivalent guard in this repo's C#, Rust, TypeScript and Java
   clients.
2. **Insecure host guard** -- `connect/3` raises `InsecureRemoteHostError` for a
   non-loopback endpoint without the opt-in (asserted with `assert_raise`, which
   proves no network call preceded the raise, since the guard runs before
   `GRPC.Stub.connect/2` is ever called), and succeeds -- carrying the token into
   the resulting channel's `headers` -- for a loopback endpoint or with the opt-in.
   The token-carrying assertion drives a real local TCP listener (no real SpiceDB
   server needed) so the check reads the actual `%GRPC.Channel{}` grpc-elixir
   returns, not a value this test constructs itself.
3. **TLS material validation** -- `insecure: true` combined with any of
   `ca_cert`/`client_cert`/`client_key` raises `ArgumentError` before any network
   call; `client_cert` without `client_key` (and the reverse) raises
   `ArgumentError`.

The handshake proof for the default-TLS-trust-source path (an actual completed TLS
handshake against a reachable server) is deferred to the idiomatic client's tests,
per root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
clause 2 -- matching Ruby's own `custom_tls_spec.rb` comment that "the handshake
proof lives in the idiomatic client's" tests.

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry a
`@deprecated "..."` module attribute on the generated module plus a runtime
`IO.warn("[DEPRECATION] ...")` call at the call site in the client wrapper.

### Invariants

- No business logic -- only plumbing (connection, auth)
- Generated files under `lib/gen/` are never modified
