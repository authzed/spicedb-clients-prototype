# SpiceDB Client Libraries

Monorepo of idiomatic SpiceDB client libraries for Go, Python, TypeScript, C#, Java, Ruby, and Rust.

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
```

**Proto clients** are generated from SpiceDB's protobuf definitions using `buf generate`. They are internal dependencies — not for direct end-user consumption.

**Idiomatic clients** wrap the proto clients with language-native APIs: native error types, iterators/async patterns for streaming, builder patterns for complex requests, and opaque ZedToken-based consistency strategies. See [DESIGN.md](DESIGN.md) for the full design vision.

## Quick Start

### Go

```go
import (
    "github.com/authzed/spicedb-clients/spicedb-go/client"
    "github.com/authzed/spicedb-clients/spicedb-go/consistency"
    "github.com/authzed/spicedb-clients/spicedb-go/rel"
)

c, err := client.NewPlaintext("localhost:50051", "somerandomkeyhere")

allowed, err := c.CheckOne(ctx, consistency.Full(), "view",
    rel.MustFromTriple("document", "readme", "view", "user", "alice", ""))
```

### Python

```python
from spicedb import SpiceDBClient, Relationship, full

async with SpiceDBClient("localhost:50051", token="somerandomkeyhere", insecure=True) as client:
    allowed = await client.check_permission(
        full(),
        Relationship.from_triple("document:readme", "view", "user:alice"),
    )
```

### TypeScript

```typescript
import { createSpiceDBClient, full } from "@spicedb/client";

const client = createSpiceDBClient("localhost:50051", "somerandomkeyhere", { insecure: true });

const allowed = await client.checkPermission(full(), {
  resourceType: "document",
  resourceId: "readme",
  permission: "view",
  subjectType: "user",
  subjectId: "alice",
});
```

### C#

```csharp
using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

var rel = Relationship.FromTriple("document", "readme", "view", "user", "alice");
bool allowed = await client.CheckPermission(Full(), "view", rel);
```

### Java

```java
import com.authzed.spicedb.*;
import static com.authzed.spicedb.Consistency.*;

try (var client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere")) {
    var rel = Relationship.of("document", "readme", "view", "user", "alice");
    boolean allowed = client.checkPermission(full(), "view", rel);
}
```

### Ruby

```ruby
require "spicedb"

SpiceDB::Client.new_plaintext("localhost:50051", "somerandomkeyhere") do |client|
  rel = SpiceDB::Relationship.from_triple("document", "readme", "view", "user", "alice")
  allowed = client.check_permission(SpiceDB::Consistency.full, "view", rel)
end
```

### Rust

```rust
use spicedb::{client::SpiceDBClient, consistency, types::Relationship};

let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere").await?;

let rel = Relationship::new("document", "readme", "view", "user", "alice", "")?;
let result = client.check_permission(consistency::full(), "view", &rel).await?;
```

## Development

Requires: [Mage](https://magefile.org), [Go 1.24+](https://go.dev), [Python 3.11+](https://python.org) with [uv](https://docs.astral.sh/uv/), [Node.js](https://nodejs.org) with [pnpm](https://pnpm.io), [.NET 8+](https://dotnet.microsoft.com), [Java 17+](https://openjdk.org) with [Gradle](https://gradle.org), [Ruby 3.2+](https://ruby-lang.org) with [Bundler](https://bundler.io), [Rust](https://rustup.rs), [Docker](https://docker.com)

### Mage targets

```bash
# Full update pipeline: generate, test, lint, commit
mage update

# Individual steps
mage gen:all              # Regenerate proto + idiomatic clients
mage test                 # Run all unit tests
mage lint:all             # Run all linters

# Per-client
cd spicedb-go && mage test
cd spicedb-go && mage lint
cd spicedb-go && mage integrationTest   # Requires Docker
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
```

Integration tests must not be run in parallel across clients (all bind to port 50051).

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
