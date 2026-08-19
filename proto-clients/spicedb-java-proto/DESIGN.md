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
- **Every `io.grpc:*` artifact — here and in `spicedb-java/lib` — resolves to one version,
  and that version is the one the BSR gRPC stubs
  (`build.buf.gen:authzed_api_grpc_java:<grpcVersion>.<n>...`) are generated against.**
  grpc-java releases its artifacts in lockstep against each other's internal SPIs and
  supports no mixture of versions.

  Both modules enforce this with `io.grpc:grpc-bom`: each declares
  `api(platform("io.grpc:grpc-bom:<version>"))` and every `io.grpc:*` coordinate is
  versionless, so there is exactly one number to change per module and a partial bump is
  structurally impossible. It is declared `api` rather than `implementation` because the
  `api` configuration does not extend `implementation`, so an implementation-scoped platform
  would not govern `api` coordinates such as lib's `grpc-api`. **When bumping, change the BOM
  version and the `authzed_api_grpc_java` BSR coordinate together** — the BSR publishes the
  same proto snapshot built against several gRPC versions, so a matching stub build is
  normally available.

  Why this is enforced structurally rather than by convention: a skew here does **not** fail
  the build, in either direction. The BSR stubs depend on `io.grpc:grpc-core` at their own
  gRPC version, so Gradle's "highest wins" silently reconciles the core cluster
  (`grpc-api`/`grpc-core`/`grpc-stub`/`grpc-protobuf`) to whichever is higher — the stubs'
  version when the declarations are lower, the declared version when they are higher (the
  case a dependency-bump PR produces). Either way the artifacts nothing else depends on —
  `grpc-netty-shaded`, `grpc-util`, `grpc-inprocess`, `grpc-testing` — are left at their own
  number, because there is no competing request to raise them. `grpc-netty-shaded` shades
  *Netty*, not gRPC, so its `io.grpc.netty` transport classes then link against a `grpc-core`
  they were not compiled against; this has already produced a `NoSuchMethodError` on
  `ManagedClientTransport$Listener.transportShutdown` thrown on a Netty event-loop thread,
  which never reaches the caller and turned prompt `UNAVAILABLE` connection failures into
  hangs that only ended at the deadline as `DEADLINE_EXCEEDED`.

  Verify by reading the *resolved* versions, not the declared ones — from `spicedb-java/`:

  ```
  gradle :lib:dependencies --configuration testRuntimeClasspath
  ```

  and from this module's own directory (`:lib` does not exist in this build):

  ```
  gradle :dependencies --configuration testRuntimeClasspath
  ```

  Every `io.grpc:*` line must show the same version. With the BOM in place the expected shape
  is `io.grpc:grpc-netty-shaded -> <version>` (a versionless request meeting a BOM constraint,
  marked `(c)`); a `X:1.72.0 -> 1.79.0` arrow means two *different* versions were requested and
  the invariant is broken.
