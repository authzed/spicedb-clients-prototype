# spicedb-java-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

This is the Java proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified. These files are regenerated on every `mage gen`.

`buf.gen.yaml` pins both plugin versions. The protoc plugin version is the
protobuf-java runtime floor of every consumer (generated classes refuse an older
runtime at class load), so it stays at the lowest version the dependencies need
and no higher than the dominant host framework manages: v33.2 (protobuf-java
4.33.2, what proto-google-common-protos 2.65 already requires; Spring Boot 4.1
manages 4.34.2). `build.gradle.kts`'s `protobufFloor` and spicedb-java's
`ProtobufRuntime.FLOOR` repeat it (a spicedb-java test compares the latter with
the stamp in `gen/`). Raise all three together, and only when a feature needs it.
The grpc-java plugin is pinned at v1.80.0 (Spring Boot 4.1.0's gRPC); the BOM
below must stay at or above it.

## Publishing

`gradle publishToMavenLocal` publishes `com.authzed.spicedb:spicedb-java-proto`
(jar, sources, POM, Gradle module metadata) at the version spicedb-java uses.
`versionMapping` writes each dependency's resolved version into the POM.

## Canonical binding

This module is the single JVM binding for `authzed.api.v1` and
`authzed.api.materialize.v0` (root DESIGN.md, "Be the single copy of the protocol
bindings in any process").

- `build.gradle.kts` compiles `gen/` (buf output, upstream `java_package`, so the
  classes are `com.authzed.api.v1.*` and `com.authzed.api.materialize.v0.*`). These
  are exactly the fully qualified names the official authzed-java
  (`com.authzed.api:authzed`) ships. The module must never depend on the BSR
  generated SDKs (`build.buf.gen:authzed_api_*`): they carry a second, independently
  generated copy of the same messages under `build.buf.gen.*`, which cannot be
  bridged (Java has no type aliases) and leaves two descriptors for one proto name
  in the process (name-keyed lookups such as `TypeRegistry`/`JsonFormat`/`Any`
  resolution then depend on which copy was registered).
- `buf.gen.yaml` also generates `protoc-gen-openapiv2/options`
  (`grpc.gateway.protoc_gen_openapiv2.options`), because no Maven artifact ships it and
  authzed-java bundles it. The remaining imports resolve to the canonical artifacts
  authzed-java itself uses: `pgv-java-stub` (`io.envoyproxy.pgv.validate`),
  `protovalidate` (`build.buf.validate`, descriptor only, non-transitive) and
  `proto-google-common-protos` (`com.google.api`, `com.google.rpc`).
- No hand-maintained files live in the generated packages. authzed-java's only
  hand-written helper, `com.authzed.grpcutil.BearerToken`, is not bound to the message
  types and stays in the bridged authzed-java jar.
- authzed-java's `.proto` sources (it ships them as jar resources) are not carried;
  `buf export buf.build/authzed/api` produces them for tools that need them.

### authzed-java bridge

Java has no type aliases, so the bridge works at the artifact level.
`gradle bridgeAuthzedJava bridgeAuthzedJavaPom` repackages the published
authzed-java jar (version pinned in `build.gradle.kts`) into
`build/bridge/authzed-<v>-spicedb-bridge.{jar,pom}`: every generated class
(`com/authzed/api/**`, `grpc/gateway/**`) and bundled `.proto` is dropped, only
`com.authzed.grpcutil` remains, and the POM depends on `spicedb-java-proto` instead.
A consumer that needs both libraries substitutes the bridged coordinate for
`com.authzed.api:authzed` (Gradle `dependencySubstitution`, or a Maven exclusion
plus the bridged artifact). The equivalent change inside authzed-java's own build is
to drop its protobuf code generation and depend on this module.

`LegacyBridgeTest` loads both in one JVM and proves:
- each shared class name (one per generated package) resolves to exactly one class
  file on the class path, loaded from this module;
- the negative control: the unbridged published jar beside this module carries two
  copies of every one of those classes;
- the bridged jar keeps `BearerToken` and nothing generated;
- code compiled against the ORIGINAL authzed-java jar (`src/legacyFixture`) runs
  unchanged on the bridged class path: its `BearerToken` authenticates and its
  `PermissionsServiceGrpc` stub calls an in-process server built from this module's
  classes, and the request/response values are the same `Class` objects.

Not proven: binary compatibility of every authzed-java method (only the fixture's
calls are linked), and authzed-java versions other than the pinned one. Generated
signatures are stable across protobuf/grpc plugin versions, but a consumer pinned to a
much older authzed-java should rerun the test with that version.

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
- Generated files under `gen/` are never modified; there are no hand-maintained files in
  generated packages (see "Canonical binding")
- **Every `io.grpc:*` artifact — here and in `spicedb-java/lib` — resolves to one version,
  at least the grpc-java plugin version that generated `gen/`'s `*Grpc.java` stubs.**
  grpc-java releases its artifacts in lockstep against each other's internal SPIs and
  supports no mixture of versions.

  Both modules enforce this with `io.grpc:grpc-bom`: each declares
  `api(platform("io.grpc:grpc-bom:<version>"))` and every `io.grpc:*` coordinate is
  versionless, so there is exactly one number to change per module and a partial bump is
  structurally impossible. It is declared `api` rather than `implementation` because the
  `api` configuration does not extend `implementation`, so an implementation-scoped platform
  would not govern `api` coordinates such as lib's `grpc-api`. **Change both modules' BOM
  version together.**

  Why this is enforced structurally rather than by convention: a skew here does **not** fail
  the build. Gradle's "highest wins" silently reconciles the core cluster
  (`grpc-api`/`grpc-core`/`grpc-stub`/`grpc-protobuf`) to the highest request, while the
  artifacts nothing else depends on — `grpc-netty-shaded`, `grpc-util`, `grpc-inprocess`,
  `grpc-testing` — stay at their own number. `grpc-netty-shaded` shades *Netty*, not gRPC,
  so its `io.grpc.netty` transport classes then link against a `grpc-core` they were not
  compiled against; this has already produced a `NoSuchMethodError` on
  `ManagedClientTransport$Listener.transportShutdown` thrown on a Netty event-loop thread,
  which never reaches the caller and turned prompt `UNAVAILABLE` connection failures into
  hangs that only ended at the deadline as `DEADLINE_EXCEEDED`. (Before this module became
  the canonical binding it depended on BSR generated SDKs, whose own `grpc-core` request
  was the competing one.)

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

## Prototype reset transport contract (2026-10-26)

The root and idiomatic client DESIGN.md define the approved API reset. This tier
remains transport plumbing: generated messages and RPC signatures are unchanged.
Preserve all services including materialize, upstream deprecations, authentication,
TLS defaults/overrides, endpoint guards and borrowed-channel ownership.

Forward caller deadlines and cancellation without replacing them with a fresh
per-attempt timeout. The idiomatic tier owns the one total operation budget and
read-only retry policy. Transport cancellation must release streams/uploads even
when no next message has arrived. Preserve complete status details and trailers.
Native idiomatic cancellation semantics are implemented above this tier.

Singular and bulk check stubs preserve with_tracing and debug_trace fields,
including recursive debug data; no transport adapter may erase them. Test real
loopback transport behavior for cancellation/security in addition to generated
stub construction. Real TLS handshake tests remain gated and run in CI.
