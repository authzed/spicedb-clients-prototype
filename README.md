# SpiceDB Client Libraries

> ⚠️ **PROTOTYPE — Not for production use.**
> These clients are in early development. APIs, types, and behaviors may change or break at any time, and bugs are expected. Do not rely on anything in this repo for production workloads. If you experiment with a client, pin to a specific commit and budget time for breakage.

Monorepo of idiomatic SpiceDB client libraries for Go, Python, TypeScript, C#, Java, Ruby, and Rust — plus [`spicedb-gen`](spicedb-gen/), a type-safe code generator that produces compile-time-checked wrappers from a `.zed` schema.

## Getting Started

Pick your language. Languages where `spicedb-gen` supports typed wrappers (**Go**, **TypeScript**, **Java**, **Python**) use the **typed client** examples — invalid permission checks, wrong subject types, and typos in resource names become compile-time errors. The remaining languages (**C#**, **Ruby**, **Rust**) use the **idiomatic client** directly.

For typed examples, first generate the wrapper from your schema:

```bash
spicedb-gen --schema schema.zed --lang <go|typescript|java|python> --out <output-path>
```

All examples below assume this schema:

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
tc := NewTypedClient(c)

// Writes — relation methods enforce valid subject types
_, err = tc.Touch(ctx,
    Document("readme").Viewer(User("alice")),
    Document("readme").Editor(User("bob")),
)

// Checks — autocomplete shows .View(), .Edit(), .Delete() on Document
allowed, err := Check(ctx, tc, consistency.Full(), Document("readme").View(), User("alice"))

// Lookups
for id, err := range LookupResources(ctx, tc, consistency.Full(), Document_View, User("alice")) { ... }
for id, err := range LookupSubjects(ctx, tc, consistency.Full(), Document("readme").View(), UserType) { ... }

// Type errors caught at compile time:
// Document("readme").Editor(Team("eng")) // ERROR: editor only allows user
```

### TypeScript (typed)

```typescript
import { full } from "@spicedb/client";
import { TypedClient, Document, User } from "./permissions";

const tc = TypedClient.create("localhost:50051", "somerandomkeyhere", { insecure: true });

// Writes — relation methods enforce valid subject types
await tc.touch(
    Document("readme").viewer(User("alice")),
    Document("readme").editor(User("bob")),
);

// Checks — autocomplete shows .view, .edit, .delete on Document
const allowed = await tc.check(full(), Document("readme").view, User("alice"));

// Lookups
for await (const id of await tc.lookupResources(full(), Document.view, User("alice"))) { ... }
for await (const id of await tc.lookupSubjects(full(), Document("readme").view, User)) { ... }

// Type errors caught at compile time:
// Document("readme").editor(Team("eng"));  // ERROR: editor only allows user
```

### Java (typed)

```java
import com.authzed.spicedb.SpiceDBClient;
import static com.authzed.spicedb.Consistency.*;
import static com.example.Permissions.*;

var tc = new TypedClient(SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere"));

// Writes — relation methods enforce valid subject types
tc.touch(
    Document("readme").viewer(User("alice")),
    Document("readme").editor(User("bob"))
);

// Checks — autocomplete shows .view(), .edit(), .delete() on Document
boolean allowed = tc.check(full(), Document("readme").view(), User("alice"));

// Type errors caught at compile time:
// Document("readme").editor(Team("eng")); // ERROR: editor only allows user
```

### Python (typed)

```python
from spicedb import full
from permissions import TypedClient, Document, User, DocumentView

tc = TypedClient.connect("localhost:50051", "somerandomkeyhere", insecure=True)
try:
    # Writes — relation methods enforce valid subject types
    await tc.touch(
        Document("readme").viewer(User("alice")),
        Document("readme").editor(User("bob")),
    )

    # Checks — autocomplete shows .view, .edit, .delete on Document
    allowed = await tc.check(full(), Document("readme").view, User("alice"))

    # Lookups
    async for rid in tc.lookup_resources(full(), DocumentView, User("alice")): ...
    async for sid in tc.lookup_subjects(full(), Document("readme").view, User): ...

    # Type errors caught at static-analysis time (pyright/mypy):
    # Document("readme").editor(Team("eng"))  # ERROR: editor only allows user
finally:
    await tc.close()
```

### C# (idiomatic)

```csharp
using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

var rel = Relationship.FromTriple("document", "readme", "viewer", "user", "alice");
await client.WriteRelationshipsAsync(Transaction.Touch(rel));

bool allowed = await client.CheckPermission(Full(), "view", rel);
```

### Ruby (idiomatic)

```ruby
require "spicedb"

SpiceDB::Client.new_plaintext("localhost:50051", "somerandomkeyhere") do |client|
  rel = SpiceDB::Relationship.from_triple("document", "readme", "viewer", "user", "alice")
  client.write_relationships(SpiceDB::Transaction.touch(rel))

  allowed = client.check_permission(SpiceDB::Consistency.full, "view", rel)
end
```

### Rust (idiomatic)

```rust
use spicedb::{client::SpiceDBClient, consistency, types::Relationship};

let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere").await?;

let rel = Relationship::new("document", "readme", "viewer", "user", "alice", "")?;
client.write_relationships(&[rel.clone()]).await?;

let allowed = client.check_permission(consistency::full(), "view", &rel).await?;
```

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
