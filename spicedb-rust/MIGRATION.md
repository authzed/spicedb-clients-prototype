# Migrating to the `spicedb` crate

There is no official `authzed-*` Rust client. Rust projects talk to SpiceDB
through a hand-written client: the authzed protos vendored into the project,
compiled by `tonic-build` or `tonic-prost-build` in a `build.rs`, a wrapper
struct with a bearer-token interceptor, and often a mock `PermissionsService`
server for tests. This guide maps that setup onto this crate, so the
vendored protos, `build.rs`, wrapper and mock can be deleted.

## Requirements

| Requirement | Floor | Notes |
|---|---|---|
| Rust | 1.82 | `rust-version` in `Cargo.toml` |
| tonic | 0.14 | with tonic-types 0.14 and tonic-prost 0.14; an application on tonic 0.12 or 0.13 must upgrade to share one copy |
| prost, prost-types | 0.14 | |
| tower | 0.5 | only with the `testing` feature |
| tokio | 1.38 | multi-thread or current-thread runtime |
| TLS | rustls with ring, native roots | `new_system_tls` / `https://` use the platform trust store; custom CAs and client certificates are not supported |
| SpiceDB | 1.30 | older servers work, but data they send only in trailers is reachable only through `ResponseMetadata` |

Run `cargo tree -d` after adding the crate: an application on tonic 0.14
should show one `tonic` and one `prost`. A second copy means some dependency
is still on an older tonic, and `tonic::Status::from(SpiceDBError)` then fails
with E0277 ("multiple different versions of crate `tonic`").

## Installing before the crate is published

The crate is not on crates.io yet. Depend on it by git (or by path to a
checkout); `spicedb-proto` comes with it:

```toml
[dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients" }

[dev-dependencies]
spicedb = { git = "https://github.com/authzed/spicedb-clients", features = ["testing"] }
```

Pin a revision (`rev = "..."`) for reproducible builds.

## Keeping your own `spicedb` crate during the migration

Projects often name their own wrapper crate `spicedb` too. To migrate one
crate at a time, keep both: depend on this crate under another dependency key
with Cargo's `package =` rename, and import it under that name.

```toml
# Cargo.toml (workspace)
[workspace.dependencies]
spicedb = { path = "spicedb" }                                # your existing wrapper
spicedb-client = { package = "spicedb", git = "https://github.com/authzed/spicedb-clients" }

# Cargo.toml (a member being migrated)
[dependencies]
spicedb-client = { workspace = true }
```

```rust
use spicedb_client::{consistency, PermissionCheck, SpiceDBClient};
```

When the old crate is gone, rename the key back to `spicedb` and the imports
to `spicedb::`. One caveat: Cargo refuses two path or git packages with the
same name *and* version ("package collision in the lockfile"). If your crate
is also `spicedb` 0.2.0, change its version (for example to `0.0.0`).

This crate keeps the name `spicedb` rather than taking a more specific one:
`spicedb` is the name an official client should own on crates.io, and the
obvious alternatives (`spicedb-client`, `spicedb-rust`) are already taken
there by unrelated crates. The rename above is one manifest line.

## Connecting

Pass the URL your deployment already uses. The scheme selects the transport.

```rust
// Before: Channel::from_shared(url)?.connect().await? plus an interceptor
// that adds `authorization: Bearer <token>` to every request.
let client = SpiceDBClient::builder(std::env::var("SPICEDB_URL")?, token)
    .connect_mode(ConnectMode::Eager) // fail at startup, as `.connect()` did
    .build()
    .await?;
```

- `http://host:port` is plaintext, `https://host[:port]` is TLS, and
  `host:port` is TLS unless you call `.plaintext()`. Any other scheme fails at
  construction with `InvalidArgument`. `new_plaintext` and `new_system_tls`
  require the scheme, if any, to agree with them.
- Ports: an `http://` URL must name its port (`http://spicedb:50051`);
  `http://spicedb` fails at construction instead of dialing port 80. An
  `https://` URL without a port uses 443. A `host:port` endpoint should name
  its port; an omitted one is the transport default (80 plaintext, 443 TLS).
- A malformed endpoint (a path such as `/v1`, a query, a fragment, `user@`,
  a bad port) fails at construction with `InvalidArgument` naming that
  problem. The remote-plaintext refusal is only for a well-formed remote host.
- A connection failure is `SpiceDBError::Transport` and reads like
  `transport error: connecting to http://spicedb:50051: tcp connect error:
  Connection refused (os error 61)`; `source()` still reaches the I/O, DNS or
  TLS error.
- Plaintext to a host that is not loopback needs
  `.remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)`;
  without it, `http://spicedb:50051` fails at construction. The loopback check
  uses the URL's host, so `http://127.0.0.1:50051` and `http://localhost` need
  nothing.
- Plaintext connects on the first call unless you choose `ConnectMode::Eager`.
  With it, a wrong or unreachable endpoint fails `build` with
  `SpiceDBError::Transport`. TLS always connects during `build` unless you
  choose `ConnectMode::Lazy`.
- If you already build a tonic `Channel` (custom TLS, in-memory transport),
  wrap it: `SpiceDBClient::from_channel(channel).token(token).build()?`.
- The bearer token is attached for you; delete the interceptor.

`SpiceDBClient` is `Clone + Send + Sync` and cheap to clone. Build it once at
startup and share it; don't reconnect per request.

## Requests and responses

Request structs with `Option` fields and `i32` enums become native values
whose constructors validate before any RPC.

| Hand-written tonic client | `spicedb` |
|---|---|
| `Consistency { requirement: Some(FullyConsistent(true)) }` | `consistency::full()` |
| `Consistency { requirement: Some(MinimizeLatency(true)) }` | `consistency::min_latency()` |
| `AtLeastAsFresh(ZedToken { token })` | `consistency::at_least(ZedToken::from(token))` |
| `AtExactSnapshot(ZedToken { token })` | `consistency::snapshot(ZedToken::from(token))` |
| `ObjectReference { object_type, object_id }` | `ObjectRef::new(object_type, object_id)?` |
| `SubjectReference { object, optional_relation: "" }` (or `"..."`) | `object.into()` or `SubjectRef::direct(object)` |
| `SubjectReference { object, optional_relation: "member" }` | `SubjectRef::new(object, Some("member".into()))?` |
| `Relationship { resource, relation, subject, .. }` | `Relationship::new(...)` or `"document:1#viewer@user:alice".parse()?` |
| `CheckPermissionRequest { .. }` / `check_permission` | `client.check(PermissionCheck::new(resource, "view", subject, consistency))` |
| `response.permissionship == Permissionship::HasPermission as i32` | `result.has_permission()` (conditional and unknown never grant) |
| `CheckBulkPermissionsRequest { items }` | `client.check_bulk(BulkCheckRequest::new(checks))`, results in input order |
| `RelationshipUpdate { operation: Touch as i32, relationship }` | `txn.touch(&relationship)` on a `Transaction` |
| `RelationshipUpdate { operation: Create / Delete }` | `txn.create(&r)` / `txn.delete(&r)` |
| `Precondition { operation: MustMatch / MustNotMatch, filter }` | `txn.must_match(filter)` / `txn.must_not_match(filter)` |
| `write_relationships(WriteRelationshipsRequest { .. })` | `client.write(WriteRequest::new(txn))` |
| `WriteRelationshipsResponse.written_at: Option<ZedToken>` | `WriteResult.revision: ZedToken` (always present) |
| `RelationshipFilter { resource_type, optional_relation, .. }` | `Filter::new(resource_type).with_relation(..)` and the other `with_*` methods |
| `delete_relationships(DeleteRelationshipsRequest { relationship_filter, .. })` | `client.delete_relationships(DeleteRequest::new(filter, limit))`, or `delete_all_relationships_non_atomic` to delete everything matching |
| `read_relationships` stream of `ReadRelationshipsResponse` | `client.read_relationships(ReadRelationshipsRequest::new(filter, consistency))`, paged for you |
| `lookup_resources` stream of `LookupResourcesResponse { resource_object_id, .. }` | `client.lookup_resources(LookupResourcesRequest::new(type, permission, subject, consistency))` yielding `LookupResource { resource_id, permissionship, .. }` |
| `lookup_subjects` stream | `client.lookup_subjects(LookupSubjectsRequest::new(resource, permission, subject_type, consistency))` |
| `ReadSchemaResponse.schema_text` | `client.read_schema(ReadSchemaRequest::new()).await?.schema_text` |
| `tonic::Status` errors | `SpiceDBError`: `code()`, `server_message()`, `reason()`; `tonic::Status::from(err)` converts back |

Anything without a native wrapper is still reachable on the same
authenticated channel through `client.raw_proto()`, with the generated types
at `spicedb::spicedb_proto`. Don't compile the protos yourself as well: two
generated copies are distinct Rust types.

### Behaviour you will notice

- Unary calls have a 30-second default deadline (`default_timeout`,
  `DefaultDeadline::Disabled`), and reads retry `Unavailable` and `Aborted`
  three times with backoff. Mutations are never replayed.
- Lookups and reads page for you, repeating your consistency on every page.
- `lookup_resources` keeps conditional results by default. Add
  `LookupResourcesOptions::new().with_permissionship_filter(LookupPermissionshipFilter::HasPermissionOnly)`
  to list only unconditional grants. A hand-written mock that set
  `CheckPermissionResponse`'s `HasPermission` value (2) on a lookup response
  was really sending `LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION`; that filter
  drops it.
- Empty IDs, types and permissions fail locally with `InvalidArgument` before
  any request. Tests that checked against default (empty) values need real
  IDs.

## Replacing a mock gRPC server

A hand-written `impl PermissionsService` behind a TCP listener becomes
`spicedb::testing::InMemorySpiceDB` (feature `testing`), which returns a real
`SpiceDBClient` over an in-memory channel. Code under test keeps taking a
`SpiceDBClient`.

```rust
use spicedb::testing::{grant_all, server_error, InMemorySpiceDB, Resolution};

// A double that grants everything, for tests over generated IDs.
let fake = InMemorySpiceDB::new();
fake.set_check_resolver(grant_all);
let client = fake.client()?;

// Or answer from stored relationships and a rule.
let fake = InMemorySpiceDB::with_relationships([
    "organization:acme#view@user:alice".parse()?,
]);
fake.set_check_resolver(|check| {
    (check.subject().object().object_id() == "admin").then(Resolution::has_permission)
});

// Failure paths: fail the next check, or any RPC at a chosen point.
fake.fail_next(Operation::Check, server_error(tonic::Code::Unavailable, "down"));
```

- A check resolves to a result registered with `set_check_result`, then to
  the resolver, then to the stored relationships (a relationship whose
  relation is the permission's name and whose subject matches, or is a
  `type:*` wildcard, grants), then to `NoPermission`. No schema is evaluated.
- `lookup_resources` and `lookup_subjects` return the stored objects of the
  requested type that check resolution grants, so a list endpoint that
  filters rows through a lookup works on the double: store a relationship
  that mentions the resource (or subject) and grant it.
- Every RPC is recorded (`calls`, `calls_of(Operation::Check)`) with its
  protocol request and metadata; `set_fault_hook` fails an RPC before it runs,
  before each import batch or commit, or before any streamed item.
- Writes, reads, deletes, imports, exports and schema reads and writes keep
  their SpiceDB semantics; `fake.relationships()` shows the store.
