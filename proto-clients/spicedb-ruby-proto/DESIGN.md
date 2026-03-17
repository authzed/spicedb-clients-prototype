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

2. **`#initialize(endpoint, token, insecure: false)`** -- constructor that:
   - Creates a `GRPC::Core::Channel` with appropriate credentials
   - Injects the bearer token via call credentials (secure) or interceptor (insecure)
   - Builds all four service stubs

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

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`warn "[DEPRECATION] ..."` calls in the client wrapper.

### Invariants

- No business logic -- only plumbing (connection, auth)
- Generated files under `lib/gen/` are never modified
