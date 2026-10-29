# spicedb-java

The idiomatic Java client for [SpiceDB](https://authzed.com/spicedb): Java
records, closeable streams, typed exceptions, one operation budget and safe
retries over the SpiceDB API. A generated typed layer (`spicedb-gen --lang
java`) turns a schema into compile-checked resources, permissions and
relations on top of it.

| Artifact | Contents |
|---|---|
| `com.authzed.spicedb:spicedb-java` | the client (`com.authzed.spicedb`), errors, `ProtoConv`, the `InMemorySpiceDB` test double |
| `com.authzed.spicedb:spicedb-java-proto` | the generated protocol classes (`com.authzed.api.v1`); a dependency of the client, not something to call directly |

Both artifacts share a group and version. Depending on `spicedb-java` brings
in the proto artifact, gRPC and protobuf-java.

## Requirements

- Java 17 or later.
- protobuf-java 4.33.2 or later at run time. Spring Boot 4.1 manages 4.34.2,
  so a Spring Boot host needs no override. If another dependency manager pins
  an older version, the first `SpiceDBClient` constructor call fails with a
  message naming the `protobuf-java.version` property to raise.
- gRPC 1.80.0 or later, one version for every `io.grpc` artifact. The client
  declares 1.84.0; a Spring Boot 4.1.0 host that manages gRPC at 1.80.0 works.

See [MIGRATION.md](MIGRATION.md#requirements) for the full list.

## Installation before publication

The artifacts are not on Maven Central yet. Install both into your local Maven
repository (`~/.m2`) from a checkout of this repository. There is no Gradle
wrapper here; use the `gradle` on your PATH (`brew install gradle`):

```bash
(cd proto-clients/spicedb-java-proto && gradle publishToMavenLocal)
(cd spicedb-java && gradle :lib:publishToMavenLocal)
```

Then depend on the one client artifact.

Maven:

```xml
<dependency>
  <groupId>com.authzed.spicedb</groupId>
  <artifactId>spicedb-java</artifactId>
  <version>0.1.0</version>
</dependency>
```

Gradle (Kotlin DSL):

```kotlin
repositories {
    mavenLocal()
    mavenCentral()
}

dependencies {
    implementation("com.authzed.spicedb:spicedb-java:0.1.0")
}
```

The published POM gives every dependency a version, so no other SpiceDB, gRPC
or protobuf declaration is needed. To share the artifacts in a team, publish
the same two builds to your own repository instead of `mavenLocal`.

## First call

```java
import com.authzed.spicedb.*;

try (var client = SpiceDBClient.createSystemTls("spicedb.example.com:443", System.getenv("SPICEDB_TOKEN"))) {
  var txn = new Transaction();
  txn.touch(Relationship.fromTuple("document:readme#viewer@user:alice"));
  WriteResult written = client.write(txn);

  boolean allowed =
      client
          .check(
              new PermissionCheck(
                  new ObjectRef("document", "readme"),
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "alice")),
                  Consistency.atLeast(written.revision())))
          .hasPermission();
}
```

Build one client at startup and share it: it is thread-safe and owns its gRPC
channel until `close()`. For local development, `createPlaintext("localhost:50051",
key)`; keepalive and other channel settings go in a customizer beside
`ClientOptions`:

```java
SpiceDBClient.createSystemTls(endpoint, key, ClientOptions.builder().build(),
    b -> b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveTimeout(10, TimeUnit.SECONDS));
```

## More

- [DESIGN.md](DESIGN.md): the API's rules and guarantees.
- [MIGRATION.md](MIGRATION.md): moving from authzed-java, with requirements
  and a member-by-member map.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): how to integrate, typed
  layer first; also the shortest tour of the API.
- [examples/](examples/README.md): runnable examples that double as
  integration tests.

## Development

```bash
gradle spotlessApply && gradle spotlessCheck :lib:test   # JAVA_HOME=$(brew --prefix openjdk@21)
```

`mage integrationTest` starts a SpiceDB container and runs the examples; see
[examples/README.md](examples/README.md).
