# SpiceDB Client Libraries

> ⚠️ **PROTOTYPE — Not for production use.**
> These clients are in early development. APIs, types, and behaviors may change or break at any time, and bugs are expected. Do not rely on anything in this repo for production workloads. If you experiment with a client, pin to a specific commit and budget time for breakage.

Monorepo of idiomatic SpiceDB client libraries for Go, Python, TypeScript, C#, Java, Ruby, and Rust — plus [`spicedb-gen`](spicedb-gen/), a type-safe code generator that produces compile-time-checked wrappers from a `.zed` schema.

> 🤖 **AI coding agents:** before integrating a client, read
> [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md). It covers which tier to use
> (typed, idiomatic or raw), plus consistency, fail-closed checks, writes and
> testing. It links each language's concrete guide.

## Getting Started

Pick your language. Languages where `spicedb-gen` supports typed wrappers (**Go**, **TypeScript**, **Java**, **Python**) use the **typed client** examples — invalid permission checks, wrong subject types, and typos in resource names become compile-time errors. The remaining languages (**C#**, **Ruby**, **Rust**) use the **idiomatic client** directly.

> **A check never returns a bare boolean.** Every client's check surface returns a
> `CheckResult` carrying a three-valued `permissionship`, the caveat context the
> server was missing, and the revision the check ran at. **Only an unconditional
> grant is true** — always go through the predicate (`HasPermission()`,
> `has_permission`, `has_permission?`, `hasPermission()`), never the result object
> itself. See [DESIGN.md](DESIGN.md#domain-types-and-checks).

For typed examples, first generate the wrapper from your schema:

```bash
spicedb-gen --schema schema.zed --lang <go|typescript|java|python> --out <output-path>
```

Tracing is optional and disabled by default; the examples enable it to make
`CheckResult` debug information available. The snippets assume the schema below
has already been written to the server:

```
definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}
```

### Go (typed)

```go
import (
    "github.com/authzed/spicedb-clients/spicedb-go/client"
    "github.com/authzed/spicedb-clients/spicedb-go/consistency"
    . "path/to/generated/permissions"
)

c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")
if err != nil { panic(err) }
defer c.Close()
tc := NewTypedClient(c)

// Writes — relation methods enforce valid subject types
written, err := tc.Touch(ctx, []TypedRelationship{
    Document("readme").Viewer(User("alice")),
    Document("readme").Editor(User("bob")),
})
if err != nil { panic(err) }

// Checks — autocomplete shows .View(), .Edit(), .Delete() on Document.
// Check returns a client.CheckResult, never a bare bool: only HasPermission()
// is a grant. A Conditional result means caveat context was missing, not "yes".
result, err := Check(ctx, tc, consistency.AtLeast(written.Revision), Document("readme").View(), User("alice"), client.WithCheckTracing(client.TracingEnabled))
if err != nil { panic(err) }
if result.HasPermission() { ... }

// Lookups
for res, err := range LookupResources(ctx, tc, consistency.Full(), Document_View, User("alice"), client.WithLookupResourcesPermissionshipFilter(client.LookupHasPermissionOnly)) { ... }
for sub, err := range LookupSubjects(ctx, tc, consistency.Full(), Document("readme").View(), UserType) {
    if err != nil { panic(err) }
    if sub.Subject.Permissionship != client.PermissionshipHasPermission { continue }
    // Handle sub, preserving any wildcard exclusions.
}

// Type errors caught at compile time:
// Document("readme").Editor(Team("eng")) // ERROR: editor only allows user
```

### TypeScript (typed)

```typescript
import { full, atLeast, Tracing, TransportSecurity, LookupPermissionshipFilter, WatchContent } from "@spicedb/client";
import { TypedClient, Document, User } from "./permissions";

const tc = TypedClient.create("localhost:50051", "somerandomkeyhere", { transportSecurity: TransportSecurity.Plaintext });

// Writes — relation methods enforce valid subject types
const written = await tc.touch([
    Document("readme").viewer(User("alice")),
    Document("readme").editor(User("bob")),
]);

// Checks — autocomplete shows .view, .edit, .delete on Document.
// check() returns a CheckResult, never a bare boolean. NEVER write `if (result)`:
// every object is truthy in JS, so that would grant on an unevaluated caveat.
const result = await tc.check(atLeast(written.revision), Document("readme").view, User("alice"), { tracing: Tracing.Enabled });
if (result.hasPermission()) { ... }

// Lookups
for await (const res of tc.lookupResources(full(), Document.view, User("alice"), { permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly })) { ... }
for await (const sub of tc.lookupSubjects(full(), Document("readme").view, User)) {
    if (sub.subject.permissionship !== "hasPermission") continue;
    // Handle sub, preserving any wildcard exclusions.
}

// Type errors caught at compile time:
// Document("readme").editor(Team("eng"));  // ERROR: editor only allows user
tc.close();
```

### Java (typed)

```java
import com.authzed.spicedb.CheckOptions;
import com.authzed.spicedb.CheckTracing;
import com.authzed.spicedb.LookupOptions;
import com.authzed.spicedb.LookupPermissionshipFilter;
import java.util.List;
import static com.authzed.spicedb.Consistency.*;
import static com.example.Permissions.*;

var tc = TypedClient.createPlaintext("localhost:50051", "somerandomkeyhere");

// Writes — relation methods enforce valid subject types
var written = tc.touch(List.of(
    Document("readme").viewer(User("alice")),
    Document("readme").editor(User("bob"))
));

// Checks — autocomplete shows .view(), .edit(), .delete() on Document.
// check() returns a CheckResult, never a bare boolean: only hasPermission()
// is a grant. A CONDITIONAL_PERMISSION result means caveat context was missing.
var result = tc.check(atLeast(written.revision()), Document("readme").view(), User("alice"),
    CheckOptions.builder().tracing(CheckTracing.ENABLED).build());
if (result.hasPermission()) { ... }

try (var resources = tc.lookupResources(full(), Document_View, User("alice"),
    LookupOptions.builder()
        .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY).build())) {
    resources.forEach(resource -> System.out.println(resource.resourceId()));
}

// Type errors caught at compile time:
// Document("readme").editor(Team("eng")); // ERROR: editor only allows user
tc.close();
```

### Python (typed)

```python
from spicedb import full, at_least, Tracing, TransportSecurity, LookupPermissionshipFilter, Permissionship
from permissions.permissions import Document, User, DocumentView
from permissions.aio import TypedClient

tc = TypedClient.connect("localhost:50051", "somerandomkeyhere", transport_security=TransportSecurity.PLAINTEXT)
try:
    # Writes — relation methods enforce valid subject types
    written = await tc.touch(
        Document("readme").viewer(User("alice")),
        Document("readme").editor(User("bob")),
    )

    # Checks — autocomplete shows .view, .edit, .delete on Document.
    # check() returns a CheckResult, never a bare bool: only .has_permission is
    # a grant. A CONDITIONAL_PERMISSION result means caveat context was missing.
    result = await tc.check(at_least(written.revision), Document("readme").view, User("alice"), tracing=Tracing.ENABLED)
    if result.has_permission: ...

    # Lookups
    async with tc.lookup_resources(full(), DocumentView, User("alice"), permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY) as resources:
        async for res in resources: ...
    async with tc.lookup_subjects(full(), Document("readme").view, User) as subjects:
        async for sub in subjects:
            if sub.subject.permissionship != Permissionship.HAS_PERMISSION:
                continue
            # Handle sub, preserving any wildcard exclusions.

    # Type errors caught at static-analysis time (pyright/mypy):
    # Document("readme").editor(Team("eng"))  # ERROR: editor only allows user
finally:
    await tc.close()
```

The four generated facades expose the native client's capabilities directly.
Schema-aware checks and relationship helpers retain their type guarantees;
other operations accept the native request/options/result types. For example:

```typescript
const deleted = await tc.deleteAllRelationshipsNonAtomic(
    { resourceType: "document", resourceId: "readme" },
    { pageSize: 100 },
);
const events = tc.watch({
    relationshipFilters: [{ resourceType: "document" }],
    startRevision: deleted.revision,
    content: [WatchContent.Relationships, WatchContent.Checkpoints],
});
```

Generated `delete` helpers delete explicit typed relationships in a transaction
and return `WriteResult`; they are separate from filter-based non-atomic deletion.
Native client access remains available for interoperability.

### C# (idiomatic)

```csharp
using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

var resource = new ObjectRef("document", "readme");
var subject = new SubjectRef(new ObjectRef("user", "alice"));
var txn = new Transaction();
txn.Touch(new Relationship(resource, "viewer", subject));
WriteResult written = await client.WriteAsync(txn);

CheckResult result = await client.CheckAsync(
    PermissionCheck.CreateBuilder()
        .WithResource(resource).WithPermission("view").WithSubject(subject)
        .WithConsistency(AtLeast(written.Revision)).Build(),
    new CheckOptions { Tracing = CheckTracing.Enabled });
if (result.HasPermission) { ... }

```

### Ruby (idiomatic)

```ruby
require "spicedb"

SpiceDB::Client.new_plaintext("localhost:50051", "somerandomkeyhere") do |client|
  resource = SpiceDB::ObjectRef.new(object_type: "document", object_id: "readme")
  subject = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: "user", object_id: "alice"))
  txn = SpiceDB::Transaction.new
  txn.touch(SpiceDB::Relationship.new(resource: resource, relation: "viewer", subject: subject))
  written = client.write(txn)

  result = client.check(
    SpiceDB::PermissionCheck.new(resource: resource, permission: "view", subject: subject),
    consistency: SpiceDB::Consistency.at_least(written.revision), tracing: SpiceDB::Tracing::ENABLED
  )
  puts result.has_permission?
end
```

### Rust (idiomatic)

```rust
use spicedb::{SpiceDBClient, consistency, ObjectRef, SubjectRef, Relationship,
    Transaction, WriteRequest, PermissionCheck, CheckOptions, TracingMode, RemoteCredentialsPolicy};

let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere", RemoteCredentialsPolicy::LoopbackOnly).await?;
let resource = ObjectRef::new("document", "readme")?;
let subject = SubjectRef::new(ObjectRef::new("user", "alice")?, None)?;
let mut txn = Transaction::new();
txn.touch(&Relationship::new(resource.clone(), "viewer", subject.clone())?);
let written = client.write(WriteRequest::new(txn)).await?;

let result = client.check(
    PermissionCheck::new(resource, "view", subject, consistency::at_least(written.revision))
        .with_options(CheckOptions::new().with_tracing(TracingMode::Enabled))
).await?;
if result.has_permission() { ... }

```

Resource lookup examples select `HasPermissionOnly` (using each language's spelling)
to exclude conditional and unknown results before presenting accessible IDs. The
client-side filter defaults to `All` when omitted; callers retaining all results
must inspect permissionship before treating a result as a grant. Limits count
results yielded after filtering, and pagination continues through skipped results.

## Tokens and connection policies

Every relationship write returns a native `WriteResult` containing its revision
(`Revision` in Go/C#, `revision()` in Java, `revision` elsewhere). Generated
relationship-write helpers return the same wrapper.

Every client uses a dedicated `ZedToken` value for revisions. Convert stored strings
explicitly and use the token's string conversion when persisting it. Authentication
credentials and pagination cursors remain distinct from revision tokens.

All clients support authenticated plaintext connections to remote hostnames and IPs
through their normal APIs. Plaintext transport alone permits loopback connections;
remote plaintext additionally requires the named opt-in below.

| Language | Explicit remote-plaintext opt-in |
| --- | --- |
| Go | `client.WithRemotePlaintextCredentials(client.RemotePlaintextAllowed)` |
| Python | `remote_credentials=RemoteCredentials.ALLOW` |
| TypeScript | `remoteCredentials: RemoteCredentials.AllowPlaintext` |
| Java | `ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()` |
| C# | `new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow }` |
| Ruby | `remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT` |
| Rust | `RemoteCredentialsPolicy::AllowInsecureRemote` in `new_plaintext` or the client builder |

## Structure

```
proto-clients/               # buf-generated proto clients (internal)
  spicedb-go-proto/
  spicedb-python-proto/
  spicedb-typescript-proto/
  spicedb-csharp-proto/
  spicedb-java-proto/
  spicedb-ruby-proto/
  spicedb-rust-proto/
spicedb-go/                  # Idiomatic Go client
spicedb-python/              # Idiomatic Python client
spicedb-typescript/          # Idiomatic TypeScript client
spicedb-csharp/              # Idiomatic C# client
spicedb-java/                # Idiomatic Java client
spicedb-ruby/                # Idiomatic Ruby client
spicedb-rust/                # Idiomatic Rust client
spicedb-gen/                 # Type-safe client code generator
```

**Proto clients** are generated from SpiceDB's protobuf definitions using `buf generate`. They are internal dependencies — not for direct end-user consumption.

**Idiomatic clients** wrap the proto clients with language-native APIs: native error types, iterators/async patterns for streaming, builder patterns for complex requests, and opaque ZedToken-based consistency strategies. See [DESIGN.md](DESIGN.md) for the full design vision.

**spicedb-gen** parses a SpiceDB schema (`.zed` file) and generates type-safe client wrappers that provide compile-time validation of resource types, permissions, relations, and subject types. Currently supports Go, TypeScript, Java, and Python.

## Development

Requires: [Mage](https://magefile.org), [Go 1.24+](https://go.dev), [Python 3.11+](https://python.org) with [uv](https://docs.astral.sh/uv/), [Node.js](https://nodejs.org) with [pnpm](https://pnpm.io), [.NET 8+](https://dotnet.microsoft.com), [Java 17+](https://openjdk.org) with [Gradle](https://gradle.org), [Ruby 3.2+](https://ruby-lang.org) with [Bundler](https://bundler.io), [Rust](https://rustup.rs), [Docker](https://docker.com)

### Mage targets

```bash
# Full update pipeline: generate, API compat check, test, lint, commit
mage update
mage updateAllowBreak      # Like update, but skip API compat checks

# Individual steps
mage gen:all              # Regenerate proto + idiomatic clients
mage test                 # Run all unit tests
mage lint:all             # Run all linters

# Per-client
cd spicedb-go && mage test
cd spicedb-go && mage lint
cd spicedb-go && mage integrationTest   # Requires Docker

# spicedb-gen
cd spicedb-gen && mage test             # Go unit tests
cd spicedb-gen && mage integrationTest  # Generate + typecheck + vitest vs SpiceDB
```

### Regenerating against a specific API revision

`mage gen:all`, `mage gen:proto`, and `mage gen:protoLang <lang>` honor `BUFTAG`,
which pins generation to an exact `buf.build/authzed/api` revision — an
`authzed/api` git SHA, a version tag such as `v1.53.0`, or `main`:

```
BUFTAG=v1.53.0 mage gen:all
```

Unset, generation uses whatever each `buf.gen.yaml` already declares, which is the
normal local-development path. Pinning applies through these root targets only;
`mage -d proto-clients/spicedb-go-proto gen` run directly does not pin — except
for `spicedb-rust-proto`, which reads `BUFTAG` straight from the environment and
so pins even when run directly.

`CI_REGENERATION=1` allows the Claude-driven generation steps to run when `CI` is
set. It is deliberately set by exactly one workflow —
`.github/workflows/regen-from-api.yaml` — which is also the only workflow holding
Claude credentials. Every other CI job must leave it unset so that `meta.yaml`'s
`gen-nodiff` check keeps asserting that generation produces no diff.

`CLAUDE_MODEL` pins the model those steps run on. Unset -- the default -- nothing
is pinned and the CLI selects a model per task, spending a cheaper one on simple
work and reserving the expensive one for the parts that need it.

Reproducibility comes from the record rather than from a pin: every run logs the
model it chose (`== model: ...`), so a past run can be reproduced by reading that
line back and setting `CLAUDE_MODEL` to it. Set it also to hold one model steady
while comparing output. It is decided in one place, `Model` in
`internal/clauderun/clauderun.go`, and every Claude invocation in this repository
goes through it.

### Integration Tests

Each idiomatic client has a `mage integrationTest` target that:
1. Starts SpiceDB via Docker Compose
2. Runs examples against the live instance
3. Tears down SpiceDB

```bash
cd spicedb-go && mage integrationTest
cd spicedb-python && mage integrationTest
cd spicedb-typescript && mage integrationTest
cd spicedb-csharp && mage integrationTest
cd spicedb-java && mage integrationTest
cd spicedb-ruby && mage integrationTest
cd spicedb-rust && mage integrationTest
cd spicedb-gen && mage integrationTest
```

Integration tests must not be run in parallel across clients (all bind to port 50051).

### API Compatibility Tools (optional — needed for `mage update`)

| Language   | Tool                          | Install                                                          |
|------------|-------------------------------|------------------------------------------------------------------|
| Go         | go-apidiff                    | `go install github.com/joelanford/go-apidiff@latest`             |
| Python     | griffe                        | `uv tool install griffe`                                         |
| TypeScript | @microsoft/api-extractor      | (dev dependency, already in package.json)                        |
| C#         | Microsoft.DotNet.ApiCompat    | `dotnet tool install --global Microsoft.DotNet.ApiCompat.Tool`   |
| Java       | japicmp                       | Download JAR from [releases](https://github.com/siom79/japicmp/releases) to `tools/japicmp.jar` |
| Rust       | cargo-semver-checks           | `cargo install cargo-semver-checks` or `brew install cargo-semver-checks` |

These tools are used by `mage update` to detect breaking API changes before tests run.
Use `mage updateAllowBreak` to skip compatibility checks when breaking changes are intentional.

### Linting

| Language   | Tool              | Command                          |
|------------|-------------------|----------------------------------|
| Go         | golangci-lint     | `golangci-lint run ./...`        |
| Python     | ruff              | `ruff check .`                   |
| TypeScript | tsc               | `tsc --noEmit`                   |
| C#         | dotnet format     | `dotnet format --verify-no-changes` |
| Java       | spotless          | `gradle spotlessCheck`           |
| Ruby       | rubocop           | `bundle exec rubocop`            |
| Rust       | clippy + rustfmt  | `cargo clippy && cargo fmt --check` |

## Bounded and non-atomic deletion

`deleteRelationships` requires a positive limit and performs one bounded request.
`deleteAllRelationshipsNonAtomic` (using each language's naming conventions)
continues in batches, forwarding each non-empty deletion cursor to the next request.
Each batch is atomic, but completed batches remain committed
if a later batch fails or cancellation occurs. Both return `DeleteResult` with a
native revision token, deleted count, completion state and optional cursor.

Failure progress reports acknowledged work; a lost response can leave the final
batch's outcome uncertain. C# exposes `exception.GetDeleteProgress()` for both
SpiceDB errors and native cancellation exceptions.
