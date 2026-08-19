# spicedb-java-proto — Design Manifest

This is the Java proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified. These files are regenerated on every `mage gen`.

## Additional Code (for Claude)

### Required Exports

Create a `SpiceDBProtoClient.java` in `src/main/java/com/authzed/spicedb/proto/`
with:

1. **`SpiceDBProtoClient` class** — implements `AutoCloseable`, wraps all
   generated gRPC blocking stubs:
   - `PermissionsServiceBlockingStub`
   - `SchemaServiceBlockingStub`
   - `WatchServiceBlockingStub`
   - `ExperimentalServiceBlockingStub`

2. **Constructor: `SpiceDBProtoClient(String endpoint, String token, boolean insecure)`**
   — creates a `ManagedChannel`, injects a bearer token via `CallCredentials`,
   and initializes all stubs.

3. **Accessor methods** — `permissions()`, `schema()`, `watch()`,
   `experimental()` returning the respective blocking stubs.

4. **`close()` method** — gracefully shuts down the `ManagedChannel`.

### Tests

Use JUnit 5 for all tests.

Create a `SpiceDBProtoClientTest.java` in
`src/test/java/com/authzed/spicedb/proto/` with:

1. **Constructor test** — verify all stubs are non-null after construction with
   `insecure=true`
2. **Channel test** — verify the channel is accessible and not shut down
3. **Close test** — verify `close()` shuts down the channel

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`@Deprecated` annotations in the client wrapper.

### Invariants

- No business logic — only plumbing (connection, auth, stub access)
- Generated files under `gen/` are never modified
- **Every `io.grpc:*` declaration — here and in `spicedb-java/lib` — carries the same
  version, and that version is the one the BSR gRPC stubs
  (`build.buf.gen:authzed_api_grpc_java:<grpcVersion>.<n>...`) are built against.**
  grpc-java releases its artifacts in lockstep against each other's internal SPIs and
  supports no mixture of versions. The trap is that a skew here does not fail the build:
  the BSR stubs depend on `io.grpc:grpc-core`, so Gradle's "highest wins" quietly pulls
  `grpc-api`/`grpc-core`/`grpc-stub`/`grpc-protobuf` up to the stubs' version no matter what
  is declared, while artifacts nothing else depends on — `grpc-netty-shaded`, `grpc-util`,
  `grpc-inprocess`, `grpc-testing` — stay stranded at the declared number. `grpc-netty-shaded`
  shades *Netty*, not gRPC, so its `io.grpc.netty` transport classes then run against a
  `grpc-core` they were not compiled against. Verify with
  `gradle :lib:dependencies --configuration testRuntimeClasspath` and read the resolved
  versions, not the declared ones.
