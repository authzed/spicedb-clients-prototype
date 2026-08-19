# spicedb-ruby-proto -- Design Manifest

This is the Ruby proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `lib/gen/` is produced by `buf generate` and MUST NOT be
manually modified. These files are regenerated on every `mage gen`.

## Additional Code (for Claude)

### Required Exports

Create a `lib/spicedb_proto/client.rb` file with:

1. **`SpiceDBProto::Client` class** -- wraps all generated gRPC service stubs:
   - `Authzed::Api::V1::PermissionsService::Stub`
   - `Authzed::Api::V1::SchemaService::Stub`
   - `Authzed::Api::V1::WatchService::Stub`
   - `Authzed::Api::V1::ExperimentalService::Stub`

2. **`#initialize(endpoint, token, insecure: false, ca_cert: nil, client_cert: nil,
   client_key: nil)`** -- constructor that:
   - Creates a `GRPC::Core::Channel` with appropriate credentials
   - Injects the bearer token via call credentials (secure) or interceptor (insecure)
   - Builds all four service stubs

   `ca_cert`/`client_cert`/`client_key` are PEM strings carrying caller-supplied
   TLS trust material, passed to
   `GRPC::Core::ChannelCredentials.new(ca_cert, client_key, client_cert)` -- the
   C-core's order puts the key *before* the certificate chain, the reverse of how
   a caller names them. All three nil is the same call the zero-argument form
   makes, so the default secure path is still pure delegation to whatever gRPC
   trusts, per root DESIGN.md, "RULE: A system-TLS constructor must reach a real
   server", clause 1. They exist because that rule permits such delegation
   *because* a caller can supply their own material when it is not enough, and
   gRPC's compiled-in `roots.pem` does not honour a CA installed in the host's
   trust store.

2a. **`.validate_tls_material!`** -- raises `ArgumentError` from `#initialize`,
   before any channel or credential is created, on `insecure` combined with any
   trust material (root DESIGN.md, "RULE: Credentials over insecure transport
   require an explicit opt-in" -- a plaintext channel would discard the material
   and ship the bearer token in cleartext behind a call site reading as though
   TLS were configured), and on `client_cert` without `client_key` or the
   reverse.

3. **`#close`** -- closes the underlying gRPC channel

4. **`SpiceDBProto::BearerTokenInterceptor`** -- a `GRPC::ClientInterceptor`
   subclass that injects the bearer token into request metadata for insecure
   channels

### Top-level Require

Create `lib/spicedb_proto.rb` that:
- Requires all generated files from `lib/gen/`
- Requires `lib/spicedb_proto/client.rb`

### Tests

Use `rspec` for all assertions.

Create `spec/client_spec.rb` with:

1. **Constructor test** -- verify `Client.new` creates a client with all four
   service stubs populated (using fake stub classes since gen/ is empty)
2. **Close test** -- verify `#close` does not raise
3. **Interceptor test** -- verify `BearerTokenInterceptor` merges auth metadata

Create `spec/custom_tls_spec.rb` with the TLS-material wiring and refusals: that
the material reaches `GRPC::Core::ChannelCredentials` in the C-core's argument
order, that no material means all three arguments are `nil` (the delegation
clause 1 of "RULE: A system-TLS constructor must reach a real server" requires),
and that every refusal above raises before a `GRPC::Core::Channel` is created.
The handshake proof itself lives in the idiomatic client's
`spec/client_custom_tls_spec.rb`, which drives a real `GRPC::RpcServer` over TLS
through this constructor -- clause 2 of that rule requires a completed
handshake, and the fake stub classes here cannot provide one.

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`warn "[DEPRECATION] ..."` calls in the client wrapper.

### Invariants

- No business logic -- only plumbing (connection, auth)
- Generated files under `lib/gen/` are never modified
