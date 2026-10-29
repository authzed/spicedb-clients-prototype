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
| `spicedb::{RetryPolicy, RetryMode, StreamRetryMode, Operation, DefaultDeadline, CallOptions, ResponseMetadata}` | client-wide and per-operation policy and the per-call transport escape hatch |
| `spicedb::protoconv` | protocol conversions (tools that print or persist protocol-shaped data only) |
| `spicedb::testing::InMemorySpiceDB` | in-memory test double (feature `testing`, use it from `[dev-dependencies]`) |
| `spicedb::spicedb_proto`, `client.raw_proto()` | raw tonic escape hatch; not for building ordinary requests |

```toml
[dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients" }

[dev-dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients", features = ["testing"] }
```

If the project already has its own crate named `spicedb` (a hand-written
wrapper over the protos), keep both during the migration by depending on this
one under another key, `spicedb-client = { package = "spicedb", git = "..." }`,
and `use spicedb_client::...`; see [MIGRATION.md](MIGRATION.md).

The crate targets tonic 0.14 and prost 0.14, so an application on that
generation links one copy and `tonic::Status::from(SpiceDBError)` compiles.
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

Endpoints are `host:port`, `http://host[:port]` or `https://host[:port]`. With
`SpiceDBClient::builder(url, token).build()` the URL's scheme selects plaintext
or TLS, so a deployment's `SPICEDB_URL=http://spicedb:50051` setting can be
passed as is (a remote `http://` host still needs
`.remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)`).
Any other form fails at construction with `InvalidArgument`.
`SpiceDBClient::new_plaintext(endpoint, token, RemoteCredentialsPolicy::LoopbackOnly)`
is for local development; a non-loopback plaintext target needs
`RemoteCredentialsPolicy::AllowInsecureRemote`. Plaintext connects on the first
call; add `.connect_mode(ConnectMode::Eager)` to the builder so a wrong or down
SpiceDB fails at startup with `SpiceDBError::Transport`. For an existing tonic `Channel`
(in-memory, embedded SpiceDB, a connection you configured), use
`SpiceDBClient::from_channel(channel).token(token).build()?`; the channel stays
yours, and dropping the client never closes it.

## Check (fail closed)

```rust
let question = PermissionCheck::new(
    ObjectRef::new("organization", org_id)?,
    "view",
    ObjectRef::new("user", user_id)?.into(), // a direct subject
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
        ObjectRef::new("user", user_id)?.into(),
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
  never replays mutations automatically. Only a touch-only write without
  preconditions (and `write_schema`) may opt in per call with
  `CallOptions::new().with_retry_policy(policy)`; every other mutation rejects
  the opt-in with `InvalidArgument` before sending anything.
  `txn.is_idempotent()` is that rule. A client-wide
  `.operation_retry_policy(Operation::Write, policy)` retries only idempotent
  writes and sends the rest once.
- An empty transaction is sent like any other: SpiceDB checks its
  preconditions and returns the current revision.

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
(`ExportedRelationship::after_result_cursor`) work the same way. Reads and
resource lookups page automatically; `with_paging(Paging::SingleRequest)` sends
exactly one request like the raw API (limit = `with_limit` or unbounded).

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
    // Never reopen watches, with no per-call option (per-call policies still win).
    .operation_retry_policy(Operation::Updates, RetryPolicy::new().with_max_retries(0))
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
response header/trailer capture, and a per-call retry policy that overrides the
client's, including stream establishment):

```rust
// Open this one supervised watch exactly once; everything else keeps retrying.
let once = CallOptions::new().with_retry_policy(RetryPolicy::new().with_max_retries(0));
let events = tool.updates(
    UpdatesRequest::new(vec![]).with_options(WatchOptions::new().with_call_options(once)),
);
```

Protocol-shaped output: `spicedb::protoconv::relationship_to_proto(&r)` and
friends, including `check_bulk_to_proto(&request, &results)` for a whole bulk
check response and `lookup_subject_to_proto`, which also fills the deprecated
fields SpiceDB still sends.

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
`ERROR_REASON_*`. Bulk-item and delete-progress errors convert to their inner
server status, so the server's message appears once.

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
// Tests over IDs generated at run time: grant every check (and lookup).
fake.set_check_resolver(spicedb::testing::grant_all);
```

Code under test keeps taking a `SpiceDBClient`; no trait is needed. The double
applies SpiceDB's write semantics (preconditions, `create` conflicts, atomic
transactions, empty writes), filtered and paged reads and exports, atomic
imports, bounded and all-or-nothing deletes, schema reads and writes, and
registered reflection results (`set_reflect_schema_result`);
`fake.relationships()` shows the store. It evaluates no schema: a check
resolves to a result registered for that exact question (`set_check_result`),
then to a resolver's answer (`set_check_resolver(|check| ...)` returning
`Some(Resolution)` or `None` to decline; `grant_all` grants everything), then to
the stored relationships (a relationship whose relation is the permission's
name, with the subject or a `type:*` wildcard, grants; a caveated one only
conditionally; expired ones never), then `NoPermission`. `lookup_resources`
and `lookup_subjects` return the stored objects of the requested type that
check resolution grants, honoring the permissionship filter, limits and
cursors. Errors come through the real client, so they have its codes and
messages, and every option is honored as sent on the wire. Build expected errors with
`SpiceDBError::new(tonic::Code::FailedPrecondition, "...")`.

Failure paths and option assertions need no hand-written server: the double
records every RPC and injects faults before any RPC, before each import batch,
before an import commits and before each read, export or lookup item.

```rust
use spicedb::testing::{server_error, FaultStage};
fake.fail_next(Operation::Check, server_error(tonic::Code::PermissionDenied, "denied"));
fake.set_fault_hook(|f| match (f.call.operation(), f.stage, f.item) {
    (Operation::ExportRelationships, FaultStage::Item, 3) => {
        Some(server_error(tonic::Code::Unavailable, "stream reset"))
    }
    _ => None,
});
let reads = fake.calls_of(Operation::ReadRelationships);
let sent = reads[0].request::<spicedb::spicedb_proto::authzed::api::v1::ReadRelationshipsRequest>();
let request_id = reads[0].metadata().get("x-request-id");
```

Each RPC is one recorded call, so client retries and pages appear separately,
and an injected `Unavailable` is retried by the client as SpiceDB's would be.
Every client method reaches the double: operations it does not model
(expand, watch, schema introspection, counters, Materialize) are
recorded, consult the fault hook, and otherwise fail with
`tonic::Code::Unimplemented` through the real client.

```rust
use spicedb::testing::{cursor_after, SchemaRevision};
// An import-batch fault sees that batch's relationships.
fake.set_fault_hook(|f| (f.stage == FaultStage::ImportBatch && f.batch.len() > 100)
    .then(|| server_error(tonic::Code::ResourceExhausted, "batch too large")));
// Resume a read or export after its first 2 matching relationships; the
// format (the decimal count) is stable for tests.
let options = ReadRelationshipsOptions::new().with_cursor(cursor_after(2));
// Emulate a server whose schema reads report no revision (empty ZedToken).
fake.set_read_schema_revision(SchemaRevision::Omitted);
```

For integration
tests, run `authzed/spicedb serve-testing` and assert both granted and denied
cases.
