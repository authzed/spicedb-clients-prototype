# spicedb (Rust)

The idiomatic asynchronous Rust client for [SpiceDB](https://authzed.com/spicedb),
built on tokio and tonic 0.14. Requests and results are native Rust values,
checks fail closed, and an in-memory test double stands in for SpiceDB in
unit tests.

## Installation

The crate is not on crates.io yet. Depend on it by git (pin a `rev` for
reproducible builds) or by path to a checkout of this repository:

```toml
[dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients" }
tokio = { version = "1", features = ["macros", "rt-multi-thread"] }

[dev-dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients", features = ["testing"] }
```

Requirements: Rust 1.82, tonic and prost 0.14 (an application on tonic 0.14
links one copy), SpiceDB 1.30 or later. If your project already has a crate
named `spicedb`, see [MIGRATION.md](MIGRATION.md#keeping-your-own-spicedb-crate-during-the-migration).

Optional features: `testing` (the in-memory double `spicedb::testing`) and
`serde` (`Serialize` for relationships, filters, transactions and tokens).

## First call

```rust,no_run
use spicedb::{consistency, ObjectRef, PermissionCheck, Relationship, SpiceDBClient, Transaction, WriteRequest};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // host:port, http://host:port (plaintext) or https://host[:port] (TLS; 443 by default).
    let client = SpiceDBClient::builder("http://localhost:50051", "somerandomkeyhere")
        .build()
        .await?;

    let viewer: Relationship = "document:readme#viewer@user:alice".parse()?;
    let mut txn = Transaction::new();
    txn.touch(&viewer);
    let written = client.write(WriteRequest::new(txn)).await?;

    let check = PermissionCheck::new(
        ObjectRef::new("document", "readme")?,
        "view",
        ObjectRef::new("user", "alice")?.into(), // a direct subject
        consistency::at_least(written.revision),
    );
    if client.check(check).await?.has_permission() {
        println!("alice can view the readme");
    }
    Ok(())
}
```

Endpoint ports: an `http://` URL must name its port (`http://spicedb:50051`,
since nothing serves SpiceDB's plaintext gRPC on port 80), an `https://` URL
without one uses 443, and a `host:port` endpoint should always name it.
Malformed endpoints fail at construction with an error naming the problem,
and connection failures name the endpoint and the cause (refused, DNS, TLS).

Build the client once and share it: `SpiceDBClient` is a cheap
`Clone + Send + Sync` handle. A remote plaintext host needs
`.remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)`, and
`.connect_mode(ConnectMode::Eager)` makes a wrong or unreachable endpoint fail
at startup instead of on the first call.

## Testing

```rust,ignore
let fake = spicedb::testing::InMemorySpiceDB::new();
fake.set_check_resolver(spicedb::testing::grant_all);
let client = fake.client()?; // a real SpiceDBClient over an in-memory channel
```

## Further reading

- [DESIGN.md](DESIGN.md): the client's contracts (deadlines, retries, paging,
  errors, transport security, the test double).
- [MIGRATION.md](MIGRATION.md): requirements, and moving from a hand-written
  tonic client and mock server over the protos.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): integration recipes for
  coding agents (and people).
- [examples/](examples/): runnable examples that double as integration tests.
- [RPC_COVERAGE.md](RPC_COVERAGE.md): every SpiceDB RPC and its native method.
- [../DESIGN.md](../DESIGN.md): the cross-language design all clients share.
