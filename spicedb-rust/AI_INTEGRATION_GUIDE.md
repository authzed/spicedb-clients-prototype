# spicedb-rust: integration guide for AI coding agents

Concrete Rust for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Everything lives in one crate, `spicedb`:

| Path | Role |
|---|---|
| `spicedb::SpiceDBClient` | native client: one request type and one options type per operation |
| `spicedb::{ObjectRef, SubjectRef, Relationship, Filter, Transaction}` | objects, subjects, relationships, filters, transactions (re-exported from `spicedb::types`) |
| `spicedb::consistency` | `full()`, `min_latency()`, `at_least(token)`, `snapshot(token)`; `ZedToken` is in the crate root |
| `spicedb::SpiceDBError`, `spicedb::error::code_of` | typed errors and `tonic::Status` interop |
| `spicedb::{RetryPolicy, RetryMode, StreamRetryMode, DefaultDeadline, CallOptions, ResponseMetadata}` | client-wide policy and the per-call transport escape hatch |
| `spicedb::protoconv` | protocol conversions (tools that print or persist protocol-shaped data only) |
| `spicedb::testing::InMemorySpiceDB` | in-memory test double (feature `testing`, use it from `[dev-dependencies]`) |
| `spicedb::spicedb_proto`, `client.raw_proto()` | raw tonic escape hatch; not for building ordinary requests |

```toml
[dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients" }

[dev-dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients", features = ["testing"] }
```

`spicedb::tonic` re-exports the tonic version the crate is built on; use it
for `tonic::Code`, metadata and channels so the versions always agree.

## Generate the typed client

Not available: `spicedb-gen` has no Rust backend. Use the native tier below
for all code, and keep permission and relation names in one module of
constants so they are not scattered as string literals.

## Connect once

```rust
let client = SpiceDBClient::new_system_tls(
    "spicedb.example.com:443",
    std::env::var("SPICEDB_TOKEN").unwrap_or_default(),
)
.await?;
// SpiceDBClient is a cheap Clone + Send + Sync handle; share it, don't reconnect.
```

`SpiceDBClient::new_plaintext(endpoint, token, RemoteCredentialsPolicy::LoopbackOnly)`
is for local development; a non-loopback plaintext target needs
`RemoteCredentialsPolicy::AllowInsecureRemote`. For an existing tonic `Channel`
(in-memory, embedded SpiceDB, a connection you configured), use
`SpiceDBClient::from_channel(channel).token(token).build()?`; the channel stays
yours, and dropping the client never closes it.

## Check (fail closed)

```rust
let question = PermissionCheck::new(
    ObjectRef::new("organization", org_id)?,
    "view",
    SubjectRef::new(ObjectRef::new("user", user_id)?, None)?,
    consistency::min_latency(),
);
match client.check(question).await {
    Ok(result) if result.has_permission() => { /* allowed */ }
    _ => return Err(Forbidden), // errors and Conditional results both deny
}
```

- Need to observe a write? Store `written.revision.as_ref()` and check with
  `consistency::at_least(ZedToken::from(stored))`.
- Several at once; results come back in input order and only
  `has_permission()` grants:

```rust
let subject: SubjectRef = format!("user:{user_id}").parse()?;
let questions = ["a", "b"]
    .iter()
    .map(|org| {
        Ok(PermissionCheck::new(
            ObjectRef::new("organization", *org)?,
            "view",
            subject.clone(),
            consistency::min_latency(),
        ))
    })
    .collect::<Result<Vec<_>, SpiceDBError>>()?;
let results = client.check_bulk(BulkCheckRequest::new(questions)).await?;
let allowed: Vec<bool> = results.iter().map(|r| r.has_permission()).collect();
```

- Permission names chosen at runtime are plain strings in the native tier;
  validate configured names at startup (for example with
  `client.reflect_schema(...)`) rather than at check time.

## List what a subject can access

```rust
let mut ids = Vec::new();
let stream = client.lookup_resources(
    LookupResourcesRequest::new(
        "organization",
        "view",
        SubjectRef::new(ObjectRef::new("user", user_id)?, None)?,
        consistency::min_latency(),
    )
    .with_options(
        LookupResourcesOptions::new()
            .with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly),
    ),
);
futures::pin_mut!(stream);
while let Some(resource) = stream.next().await {
    ids.push(resource?.resource_id);
}
```

Omit the filter only if you handle `Permissionship::ConditionalPermission`
explicitly.

## Write atomically

```rust
let admin = |user: &str| -> Result<Relationship, SpiceDBError> {
    format!("organization:{org_id}#admin@user:{user}").parse()
};
let member: Relationship = format!("organization:{org_id}#member@user:{user_id}").parse()?;
let mut txn = Transaction::new();
// Invariant: another admin remains.
txn.must_match(admin(other_admin)?.filter());
txn.delete(&admin(user_id)?);
txn.touch(&member);
match client.write(WriteRequest::new(txn)).await {
    Ok(written) => written.revision,
    Err(SpiceDBError::FailedPrecondition(_)) => return Err(LastAdmin),
    Err(e) => return Err(e.into()),
};
```

- `touch` upserts; `create` fails with `SpiceDBError::AlreadyExists` if present.
- Deletes match by tuple identity: build the relationship without caveat or
  expiration (`"invitation:i1#inviter@user:bob".parse()?`) and `txn.delete(&r)`;
  never invent an expiration to delete one.
- Delete what you just read: `txn.delete(&item.relationship)`.
- To delete by filter, use `delete_relationships(DeleteRequest::new(filter, limit))`
  (bounded, one RPC) or `delete_all_relationships_non_atomic` (pages; not atomic).
- **Do not retry a failed write blindly**; it may have committed. The client
  never replays mutations.

## Read relationships

```rust
let mut admins = Vec::new();
let mut last_cursor = None;
let stream = client.read_relationships(ReadRelationshipsRequest::new(
    Filter::new("organization").with_resource_id(org_id).with_relation("admin"),
    consistency::min_latency(),
));
futures::pin_mut!(stream);
while let Some(item) = stream.next().await {
    let item = item?;
    admins.push(item.relationship.subject_id().to_string());
    last_cursor = item.after_result_cursor; // item.read_at is the revision
}
```

To resume, repeat the **same request and page size** with
`ReadRelationshipsOptions::new().with_cursor(cursor)`. Lookups
(`LookupResource::after_result_cursor`) and exports
(`ExportedRelationship::after_result_cursor`) work the same way.

## Schema-agnostic code (native tier)

```rust
let r: Relationship = "document:readme#viewer@user:alice".parse()?;
let mut txn = Transaction::new();
txn.touch(&r);
let written = client.write(WriteRequest::new(txn)).await?;
let result = client
    .check(PermissionCheck::new(
        r.resource().clone(),
        "view",
        r.subject().clone(),
        consistency::at_least(written.revision),
    ))
    .await?;
```

Constructors return `Result`; in tests and for constants, `.expect("...")` is
the asserting form (there are no panicking `must_*` variants). Tuples cover
SpiceDB's full syntax, including `[caveat:{...}]` and `[expiration:...]`, and
`to_string()` formats them back.

Tools with their own policies:

```rust
let tool = SpiceDBClient::from_channel(channel)
    .token(token)
    .retry_policy(RetryPolicy::new().with_max_retries(2).with_on_retry(|e| {
        eprintln!("retrying in {:?}: {}", e.delay, e.error)
    }))
    // or .retries(RetryMode::Disabled)
    .default_deadline(DefaultDeadline::Disabled)
    .stream_retries(StreamRetryMode::Disabled)
    .build()?;
let captured = ResponseMetadata::new();
tool.read_schema(ReadSchemaRequest::new().with_options(
    ReadSchemaOptions::new()
        .with_call_options(CallOptions::new().with_response_metadata(captured.clone())),
))
.await?;
let version = captured.headers().get("io.spicedb.debug.version").cloned();
```

Every operation's options take `with_call_options` (request metadata,
response header/trailer capture). Protocol-shaped output:
`spicedb::protoconv::relationship_to_proto(&r)` and friends.

## Errors

```rust
match err {
    SpiceDBError::FailedPrecondition(_) => { /* precondition failed */ }
    SpiceDBError::OutOfRange(_) => { /* stale ZedToken: drop it, re-read with consistency::full() */ }
    SpiceDBError::Unavailable(_) => { /* transient; reads were already retried */ }
    other => return Err(other.into()),
}
```

`err.code()` is the matching `tonic::Code` for every error, including local
validation and deadlines; `tonic::Status::from(err)` converts it, and
`spicedb::error::code_of(&e)` classifies any error, including a raw
`tonic::Status`. Forward `err.server_message()`, not `err.to_string()`, when
relaying an error to another party; `err.reason()` is SpiceDB's
`ERROR_REASON_*`.

## Test

```rust
let fake = InMemorySpiceDB::with_relationships([
    "organization:acme#admin@user:alice".parse::<Relationship>()?,
]);
fake.set_check_result(
    &"organization:acme".parse()?,
    "view",
    &"user:alice".parse()?,
    Permissionship::HasPermission,
);
let client = fake.client()?; // a real SpiceDBClient over an in-memory channel
```

Code under test keeps taking a `SpiceDBClient`; no trait is needed. The double
applies SpiceDB's write semantics (preconditions, `create` conflicts, atomic
transactions), filtered and paged reads, bounded and all-or-nothing deletes,
and registered check results (default `NoPermission`); `fake.relationships()`
shows the store. Build expected errors with
`SpiceDBError::new(tonic::Code::FailedPrecondition, "...")`. For integration
tests, run `authzed/spicedb serve-testing` and assert both granted and denied
cases.
