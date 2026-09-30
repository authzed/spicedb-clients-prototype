# spicedb-elixir: Idiomatic Elixir Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`): read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Elixir-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. It should
read like hand-written Elixir: plain structs, `{:ok, value}` / `{:error, exception}`
tuples, `!` variants that raise, lazy `Stream`s, and a trailing keyword list for
options. It is not a thin wrapper around gRPC, and no gRPC type appears in the
primary surface.

### Package & Module

- **Hex package / OTP app**: `spicedb`
- **Top-level module**: `SpiceDB`
- **Minimum Elixir**: 1.16 (developed and verified on 1.18 / OTP 27)
- **Proto tier**: `spicedb_proto` (`../proto-clients/spicedb-elixir-proto`), a path
  dependency. It supplies the generated `Authzed.Api.*` modules and
  `SpicedbProto.Client`, which owns channel construction and the insecure-host guard.
- **Transport**: grpc-elixir (`grpc` 1.0), through the proto tier.

### Module Structure

| Module | Role |
|--------|------|
| `SpiceDB` | Every RPC wrapper, constructors, `close/1`, `proto_client/1` |
| `SpiceDB.Client` | Opaque connected client value |
| `SpiceDB.Consistency` | Read consistency constructors |
| `SpiceDB.Relationship`, `SpiceDB.ObjectRef`, `SpiceDB.SubjectRef` | Relationship and reference values |
| `SpiceDB.Filter` | Relationship filter builder |
| `SpiceDB.Transaction` | Write builder: updates plus preconditions |
| `SpiceDB.CheckResult` | Check outcome with `has_permission?/1` |
| `SpiceDB.LookupResource`, `SpiceDB.LookupSubject`, `SpiceDB.ResolvedSubject`, `SpiceDB.PartialCaveatInfo` | Lookup results |
| `SpiceDB.ExpandResult`, `SpiceDB.PermissionTree`, `SpiceDB.IntermediateNode`, `SpiceDB.LeafNode` | Expand results |
| `SpiceDB.WatchEvent`, `SpiceDB.Update` | Watch events |
| `SpiceDB.ReflectSchemaResult`, `SpiceDB.Schema*`, `SpiceDB.RelationReference`, `SpiceDB.SchemaDiff` | Schema reflection |
| `SpiceDB.CountResult` | Relationship counter results |
| `SpiceDB.RoaringLookupResourcesResult`, `SpiceDB.PermissionChange`, `SpiceDB.WatchedPermission`, `SpiceDB.PermissionSetChange`, `SpiceDB.SetReference`, `SpiceDB.MemberReference`, `SpiceDB.PermissionSetsCursor`, `SpiceDB.PermissionSetsDownload` | Materialize tier results |
| `SpiceDB.Error` and one `SpiceDB.*Error` per status kind | Typed errors |

Everything else (`SpiceDB.Wire`, `SpiceDB.Streaming`, `SpiceDB.Retry`,
`SpiceDB.CaveatContext`) is `@moduledoc false` and private.

All RPC wrappers live on `SpiceDB` rather than on per-service modules, so a caller
learns one module and every call reads `SpiceDB.verb(client, ...)`.

### Constructors

```elixir
{:ok, client} = SpiceDB.new_plaintext("localhost:50051", token)
{:ok, client} = SpiceDB.new_system_tls("grpc.authzed.com:443", token)
{:ok, client} = SpiceDB.new_custom_tls("spicedb.internal:50051", token, ca_cert: pem)

client = SpiceDB.new_plaintext!("localhost:50051", token, default_timeout: 5_000)
:ok = SpiceDB.close(client)
```

Each constructor has a `!` variant. Options are validated with `Keyword.validate!/2`,
so an unknown option raises `ArgumentError` at the call site instead of being ignored.

- `new_plaintext/3` refuses a non-loopback endpoint with
  `SpiceDB.InvalidArgumentError` (root DESIGN.md, "RULE: Credentials over insecure
  transport require an explicit opt-in"). The opt-in is named
  `allow_insecure_remote_credentials: true`. The check itself lives in the proto
  tier (`SpicedbProto.Client.loopback_endpoint?/1`), and this tier maps its
  `SpicedbProto.InsecureRemoteHostError` to `InvalidArgumentError`.
- Connecting is eager. grpc-elixir dials when the channel is created, so a
  constructor against an unreachable host returns `{:error, %SpiceDB.UnavailableError{}}`
  rather than a client that fails later. Measured against a black-holed remote
  host, that takes about 20 seconds (the HTTP/2 client's connect timeout).
- Connecting sets `trap_exit` on the calling process. That is a side effect of
  grpc-elixir's HTTP/2 adapter, not a choice made here, and the connection is
  linked to the process that created it. Create the client in a long-lived
  process (a supervised GenServer, or the application start) and share the value.
  A `SpiceDB.Client` is an immutable struct and is safe to pass between processes.

#### Custom TLS trust material

```elixir
# A SpiceDB behind a private or corporate CA
{:ok, client} = SpiceDB.new_custom_tls(endpoint, token, ca_cert: File.read!("ca.pem"))

# ...and where the server requires mutual TLS
{:ok, client} =
  SpiceDB.new_custom_tls(endpoint, token,
    ca_cert: File.read!("ca.pem"),
    client_cert: File.read!("client.pem"),
    client_key: File.read!("client-key.pem")
  )
```

`ca_cert:` is required and must be PEM text containing at least one certificate;
anything else is an `InvalidArgumentError` before any connection is attempted.
Material is passed as PEM strings rather than file paths, so a caller reading
from a secret store never has to write key material to disk.

### Consistency

Every read takes a `SpiceDB.Consistency` as its second argument. There is no
default and no nil shortcut:

```elixir
SpiceDB.Consistency.full()
SpiceDB.Consistency.min_latency()
SpiceDB.Consistency.at_least(zed_token)
SpiceDB.Consistency.snapshot(zed_token)
SpiceDB.Consistency.at_least_or_full(maybe_token)        # nil or "" gives full()
SpiceDB.Consistency.at_least_or_min_latency(maybe_token) # nil or "" gives min_latency()
```

ZedTokens are plain `String.t()` (`SpiceDB.zed_token()`), opaque to the caller.

### Relationships

```elixir
rel = SpiceDB.Relationship.from_tuple!("document:readme#viewer@user:alice")
rel = SpiceDB.Relationship.from_triple("document", "readme", "viewer", "user", "alice", "")

rel
|> SpiceDB.Relationship.with_caveat("ip_allowlist", %{"cidr" => "10.0.0.0/8"})
|> SpiceDB.Relationship.with_expiration(~U[2027-01-01 00:00:00Z])
```

`Relationship` implements `String.Chars`, so `to_string(rel)` gives the tuple form
back. `Relationship.to_filter/1` gives the exact-match filter for a relationship.

Filters use a pipe-friendly builder:

```elixir
SpiceDB.Filter.new("document")
|> SpiceDB.Filter.with_resource_id_prefix("team-")
|> SpiceDB.Filter.with_subject_type("user")
```

A filter whose subject constraint cannot be expressed on the wire (a subject id or
relation without a subject type) is refused with `InvalidArgumentError`.

### Checks

```elixir
{:ok, result} = SpiceDB.check_permission(client, consistency, "view", rel)
SpiceDB.CheckResult.has_permission?(result)

{:ok, results} = SpiceDB.check_permissions(client, consistency, "view", rels)
{:ok, true} = SpiceDB.check_any(client, consistency, "view", rels)
{:ok, false} = SpiceDB.check_all(client, consistency, "view", [])
```

- `CheckResult.permissionship` is an atom: `:has_permission`, `:no_permission`,
  `:conditional_permission`, or `:unspecified`. Any value this client does not
  know maps to `:unspecified`, never to a grant.
- `has_permission?/1` is an equality check against `:has_permission`.
  `:conditional_permission` is not a grant; `missing_context` names the absent
  caveat parameters.
- Every check, including the single one, goes through `CheckBulkPermissions`, in
  chunks of 1 000. A response whose pair count differs from the request is an error,
  never a silently misaligned result list.
- `check_any/5` and `check_all/5` of an empty list are both `false`. An empty
  `check_all` is not vacuously true, because an authorization question with no
  subjects should never answer "yes".
- A per-item error in the bulk response fails the whole call with that item's typed
  error.

#### Supplying caveat context

```elixir
# Call-level: applies to every relationship checked in this call.
SpiceDB.check_permissions(client, consistency, "view", rels, context: %{"now" => "2026-09-30"})

# Per-item: overrides the call-level default for just this relationship.
rel = SpiceDB.Relationship.with_check_context(rel, %{"ip" => "10.1.2.3"})
```

The two maps are merged per key, the item's value winning. Keys may be atoms or
strings; they are sent as strings. Values may be `nil`, booleans, numbers,
strings (valid UTF-8), lists and maps, or a `Google.Protobuf.Value` passed
through. Anything else (a tuple, a PID, a `DateTime`, a non-UTF-8 binary) fails with
`InvalidArgumentError` whose message names the offending key path, before any
request is sent. `check_context` is never written; write-time context goes in
`caveat_context` via `with_caveat/3`.

### Lookups

```elixir
subject = SpiceDB.SubjectRef.new("user", "alice")
{:ok, stream} = SpiceDB.lookup_resources(client, consistency, "document", "view", subject)
ids = Enum.map(stream, & &1.resource_id)

resource = SpiceDB.ObjectRef.new("document", "readme")
{:ok, stream} = SpiceDB.lookup_subjects(client, consistency, resource, "view", "user")
```

`LookupSubject.excluded_subjects` is populated from the current field, falling back
to the deprecated `excluded_subject_ids` when a server sends only that.

### Streaming & Transparent Cursor Pagination

`read_relationships/4`, `lookup_resources/6`, `lookup_subjects/6`,
`export_relationships/3`, `watch/3` and the experimental streaming calls return
`{:ok, stream}`, where `stream` is a lazy `Stream`. Cursors never appear in the
API: `read_relationships/4`, `lookup_resources/6` and `export_relationships/3`
page internally (512 per page), sending each page's last cursor as the next
request's cursor until a short page arrives.

Establishment is eager for everything but the watch family: the call opens the
first page and reads its first message before returning, so a bad filter or a
missing definition comes back as `{:error, _}` from the call itself rather than
as a raise during enumeration. An error after that raises the typed error in the
enumerating process. Enumerating the same stream a second time issues the request
again.

#### Stream lifecycle: an accepted exception for server-streaming RPCs

Root `DESIGN.md`, "RULE: Abandoning a stream must release it", requires that
stopping early tells the server to stop, and requires verifying that against
the transport the client actually ships. This client calls
`Authzed.Api.*.Stub` functions directly, like every other tier of this
codebase, and verification against grpc-elixir 1.0.5 (the Mint adapter) found
no supported way to satisfy the rule for server-streaming RPCs:

- `GRPC.Stub`'s generated server-streaming function returns only an enumerable
  of replies; the `GRPC.Client.Stream` struct that `GRPC.Stub.cancel/1` needs
  to reset the HTTP/2 stream is not part of that return value and grpc-elixir
  exposes no other public function that accepts an enumerable already in
  flight and cancels it.
- Measured against a real SpiceDB, counted server-side with
  `grpc_server_started_total` and `grpc_server_handled_total`: taking one event
  from `Authzed.Api.V1.WatchService.Stub.watch/2` and dropping the enumerable
  leaves the server stream open (in-flight 1) even after the calling task
  exits. The server only sees it end, as `Canceled`, when the underlying
  channel closes.

This client previously worked around the gap with a transport shim
(`SpiceDB.Transport.GRPC.open_stream/4`) that reassembled the `GRPC.Client.Stream`
`GRPC.Stub` drops, purely so it could call `GRPC.Stub.cancel/1` on it. That shim
meant every RPC went through a dispatch layer of its own instead of the
generated stubs, which is a larger and more surprising deviation than the gap
it closed, so this client accepts the gap instead: **abandoning a stream from
`read_relationships/4`, `lookup_resources/6`, `lookup_subjects/6`,
`export_relationships/3`, `watch/3`, or an experimental streaming call leaves
the underlying HTTP/2 stream open until the connection it was issued on is
closed.** `Enum.take/2`, `Stream.take_while/2`, an exception, or simply never
enumerating a returned stream to exhaustion all stop the client from pulling
further messages, but none of them tell the server to stop sending them; the
server notices only when `SpiceDB.close/1` tears down the channel.

Bounded reads (`read_relationships/4`, `lookup_resources/6`,
`lookup_subjects/6`, `export_relationships/3`) rarely feel this: the page size
is small enough, and the server fast enough, that the page finishes before a
caller's early exit would matter. The leak is real for the open-ended streams
(`watch/3` and the experimental `WatchPermissions`/`WatchPermissionSets`
calls), where a caller that takes a few events and stops leaves that dispatch
running on the server for as long as the client connection stays open.

### Writes

Transaction builder, pipe-friendly:

```elixir
txn =
  SpiceDB.Transaction.new()
  |> SpiceDB.Transaction.create(rel)
  |> SpiceDB.Transaction.touch(other)
  |> SpiceDB.Transaction.delete(stale)
  |> SpiceDB.Transaction.must_not_match(filter)
  |> SpiceDB.Transaction.must_match(filter)

{:ok, zed_token} = SpiceDB.write_relationships(client, txn)
```

Writes are never retried: a lost response to a committed write would come back as
a confident, wrong error.

Bulk loading is a separate call, `import_relationships/3`, which takes any
enumerable (including a lazy one) and streams it in batches of 1 000 over one
client-streaming `ImportBulkRelationships` call. It is all-or-nothing: an existing
relationship fails the whole call with `AlreadyExistsError`.

### Deletions

```elixir
{:ok, zed_token} =
  SpiceDB.delete_relationships(client, filter,
    must_match: [guard],
    must_not_match: [other_guard],
    limit: 500
  )
```

Deletes page through the filter (1 000 per request by default) with partial
deletions allowed, sending each `DELETION_PROGRESS_PARTIAL` response's
`after_result_cursor` back as the next request's cursor. A PARTIAL response with no
cursor still continues: the in-memory datastore sends none, and repeating the
filter is correct because the relationships already deleted no longer match it.
Preconditions are re-sent with every page. A failure part-way leaves the earlier
pages deleted.

### Error Handling

Every call returns `{:ok, value}` or `{:error, exception}`; the `!` variant returns
the value or raises the same exception. Each gRPC status kind is its own exception
module:

| Code | Module |
|------|--------|
| 1 `CANCELLED` | `SpiceDB.CancelledError` |
| 3 `INVALID_ARGUMENT` | `SpiceDB.InvalidArgumentError` |
| 4 `DEADLINE_EXCEEDED` | `SpiceDB.DeadlineExceededError` |
| 5 `NOT_FOUND` | `SpiceDB.NotFoundError` |
| 6 `ALREADY_EXISTS` | `SpiceDB.AlreadyExistsError` |
| 7 `PERMISSION_DENIED` | `SpiceDB.PermissionDeniedError` |
| 8 `RESOURCE_EXHAUSTED` | `SpiceDB.ResourceExhaustedError` |
| 9 `FAILED_PRECONDITION` | `SpiceDB.FailedPreconditionError` |
| 11 `OUT_OF_RANGE` | `SpiceDB.OutOfRangeError` |
| 14 `UNAVAILABLE` | `SpiceDB.UnavailableError` |
| 16 `UNAUTHENTICATED` | `SpiceDB.UnauthenticatedError` |
| anything else | `SpiceDB.Error` |

Every kind carries `message`, `code` (the original integer), and the
`google.rpc.ErrorInfo` detail as `reason`, `reason_domain` and `reason_metadata`,
so a caller branches on `reason` (e.g. `"ERROR_REASON_COUNTER_NOT_REGISTERED"`)
instead of parsing a message. A transport failure with no status (a refused
connection, a reset) is an `UnavailableError`.

Elixir exceptions have no inheritance, so there is no shared parent to rescue.
Rescue the kinds you handle by name (`rescue e in [SpiceDB.UnavailableError,
SpiceDB.DeadlineExceededError]`), or match `{:error, %mod{}}` on the non-raising
call. `SpiceDB.Error.kinds/0` lists every module for a caller that needs the full
set at runtime.

Caller mistakes found before any request (a remote plaintext host, bad TLS
material, an unrepresentable caveat value, an inexpressible filter) are
`InvalidArgumentError` with `code: nil`. Unknown options raise `ArgumentError`
from `Keyword.validate!/2`, since that is a programming error, not a runtime one.

Reads retry on `UNAVAILABLE` (14) and `ABORTED` (10), up to 3 retries, with full
jitter backoff (`rand(0, 100ms * 2^(attempt - 1))`). Writes, deletes, imports,
schema writes and counter registration never retry. `RESOURCE_EXHAUSTED` is never
retried: SpiceDB uses it for load shedding and depth limits, and retrying either
makes things worse.

#### Retrying a stream: establishment only

A read stream's establishment (opening a page and reading its first message) is
retried like any other read, including each later page's establishment. A failure
in the middle of a page is never retried, because items have already been handed
to the caller: retry the open, never the middle.

`watch/3` and the experimental watch calls retry neither. They are open-ended, and
a caller that wants one re-established should decide that itself, resuming from
the last event's `changes_through`.

`watch/3` also does not read before returning: SpiceDB sends nothing on a watch
until the first change, so waiting for a first message would block the call
indefinitely. The request is sent before `watch/3` returns, but the call still
blocks long enough to learn whether the server accepted the stream at all, so a
request SpiceDB rejects outright (a malformed or out-of-window start revision,
say) comes back as `{:error, _}` from `watch/3` itself, not as a raise. A
failure the server discovers only after accepting the stream still raises from
the enumerating process.

### Deadlines

Every unary call takes `timeout:` in milliseconds (or `:infinity`). Each attempt
gets the full window, so a retried call can take up to
`timeout * (retries + 1)` plus backoff, and each delete page gets its own window.
The client's `default_timeout:` (30 000 ms unless set at construction) applies when
a call omits `timeout:`, per root DESIGN.md, "RULE: A unary call must have a
deadline".

```elixir
client = SpiceDB.new_plaintext!("localhost:50051", token, default_timeout: 5_000)
SpiceDB.check_permission(client, cs, "view", rel)                 # bound by 5 s
SpiceDB.check_permission(client, cs, "view", rel, timeout: 1_000) # overrides it
```

Server-streaming calls take no `timeout:` and are not bound by the default: a
watch may run for the life of the process, and a large export legitimately runs
longer than any unary default.

`import_relationships/3` takes `timeout:`, but it defaults to `:infinity`, not to
`default_timeout`, since its duration scales with the caller's dataset. On expiry
the client cancels the stream and returns `DeadlineExceededError`.

### Complete Method List

Every function below also exists with a `!` suffix that returns the unwrapped value
or raises. `timeout:` is accepted by every unary call.

**Client:**
- `new_plaintext(endpoint, token, opts)`: `:default_timeout`, `:allow_insecure_remote_credentials`
- `new_system_tls(endpoint, token, opts)`: `:default_timeout`
- `new_custom_tls(endpoint, token, opts)`: `:ca_cert` (required), `:client_cert`, `:client_key`, `:default_timeout`
- `close(client)` returns `:ok`
- `proto_client(client)` returns `SpicedbProto.Client.t()`

**Checks:**
- `check_permission(client, consistency, permission, relationship, opts)` returns `CheckResult.t()`; `:context`, `:timeout`
- `check_permissions(client, consistency, permission, relationships, opts)` returns `[CheckResult.t()]`
- `check_any(client, consistency, permission, relationships, opts)` returns `boolean()`
- `check_all(client, consistency, permission, relationships, opts)` returns `boolean()`

**Relationships:**
- `write_relationships(client, transaction, opts)` returns `zed_token()`
- `read_relationships(client, consistency, filter, opts)` returns a stream of `Relationship.t()`
- `delete_relationships(client, filter, opts)` returns `zed_token()`; `:must_match`, `:must_not_match`, `:limit`, `:timeout`

**Lookups:**
- `lookup_resources(client, consistency, resource_type, permission, subject_ref, opts)` returns a stream of `LookupResource.t()`; `:context`
- `lookup_subjects(client, consistency, object_ref, permission, subject_type, opts)` returns a stream of `LookupSubject.t()`; `:context`, `:subject_relation`

**Expand:**
- `expand_permission_tree(client, consistency, object_ref, permission, opts)` returns `ExpandResult.t()`

**Bulk:**
- `import_relationships(client, enumerable, opts)` returns `non_neg_integer()` (loaded count); `:timeout`, default `:infinity`
- `export_relationships(client, consistency, opts)` returns a stream of `Relationship.t()`; `:filter`

**Watch:**
- `watch(client, object_types, opts)` returns a stream of `WatchEvent.t()`; `:start_revision`, `:include_checkpoints`, `:filters`

`WatchEvent` has `updates` (a list of `SpiceDB.Update` with `operation` in
`:create | :touch | :delete | :unspecified`), `changes_through` (always populated;
pass it as `start_revision:` to resume after a dropped stream) and `is_checkpoint`.

**Schema:**
- `read_schema(client, opts)` returns `{schema_text, zed_token}`
- `write_schema(client, schema_text, opts)` returns `zed_token()`
- `reflect_schema(client, consistency, opts)` returns `ReflectSchemaResult.t()`
- `computable_permissions(client, consistency, definition, relation, opts)` returns `{[RelationReference.t()], zed_token}`; `:definition_filter`
- `dependent_relations(client, consistency, definition, permission, opts)` returns `{[RelationReference.t()], zed_token}`
- `diff_schema(client, consistency, comparison_schema, opts)` returns `{[SchemaDiff.t()], zed_token}`

`SchemaDiff.kind` is an atom named after the proto oneof case
(`:definition_added`, `:permission_expr_changed`, `:caveat_parameter_type_changed`,
...), and `:unknown` for a case this client does not know.

**Experimental (relationship counters):**
- `experimental_register_relationship_counter(client, name, filter, opts)` returns `:ok`
- `experimental_count_relationships(client, name, opts)` returns `CountResult.t()`
- `experimental_unregister_relationship_counter(client, name, opts)` returns `:ok`

**Experimental (materialize tier, `authzed.api.materialize.v0`):**
- `experimental_count_relationships_by_filter(client, filter, opts)` returns `CountResult.t()`
- `experimental_roaring_lookup_resources(client, consistency, resource_type, permission, subject_ref, opts)` returns `RoaringLookupResourcesResult.t()` (`bitmap`, `cardinality`, `at_revision`)
- `experimental_watch_permissions(client, watched_permissions, opts)` returns a stream of `{:change, PermissionChange.t()}` or `{:completed_revision, zed_token}`; `:start_revision`
- `experimental_watch_permission_sets(client, opts)` returns a stream of `{:change, _}`, `{:completed_revision, _}`, `{:lookup_permission_sets_required, _}` or `{:breaking_schema_change, _}`; `:start_revision`
- `experimental_lookup_permission_sets(client, opts)` returns a stream of `{PermissionSetChange.t(), PermissionSetsCursor.t()}`; `:limit`, `:at_revision`, `:after`
- `experimental_download_permission_sets(client, opts)` returns `PermissionSetsDownload.t()`; `:at_revision`

Materialize oneofs are tagged tuples rather than structs with one populated field,
so a caller pattern-matches the variant directly. An unknown variant is
`{:unknown, nil}`.

`authzed/spicedb:latest` does not serve the materialize services today; it answers
`UNIMPLEMENTED`, which surfaces as `%SpiceDB.Error{code: 12}`.

### Escape Hatches

`SpiceDB.proto_client/1` returns the `SpicedbProto.Client` this client makes its
own calls through. Its `channel` field is a `GRPC.Channel` that the generated stubs
accept directly:

```elixir
%SpicedbProto.Client{channel: channel} = SpiceDB.proto_client(client)
{:ok, resp} = Authzed.Api.V1.PermissionsService.Stub.check_permission(channel, request)
```

This is clearly marked **secondary** API, which root DESIGN.md's "What NOT To Do"
permits. It exists so a request the idiomatic surface cannot express (an RPC or
proto field not wrapped here, such as
`WriteRelationshipsRequest.optional_transaction_metadata`, or the single-check
`CheckPermission` RPC that `check_permission/5` routes around) has a workaround
short of forking.

- **The bearer token comes free.** The channel carries it, so a raw call is
  authenticated exactly as an idiomatic one.
- **A raw call is a raw call.** No typed error mapping (you get a
  `GRPC.RPCError`), no retry, no default timeout (pass `timeout:` yourself), and
  no stream release: see "Stream lifecycle" above for what abandoning a raw stub
  stream costs.
- **The connection belongs to the client.** `SpiceDB.close/1` releases it, and
  closing the channel from here breaks every later call.
- **It is an accessor, never a constructor.** It takes only the client, so channel
  construction stays on the single guarded path and cannot become a way around the
  insecure-transport opt-in.

No stability promise beyond grpc-elixir's and the generated code's.

### Deviations from spicedb-ruby

- Results are `{:ok, _}` / `{:error, _}` tuples with `!` variants, not raise-only.
- Options are a trailing keyword list validated with `Keyword.validate!/2`; there
  are no positional optional arguments.
- The write call is `write_relationships/3`, matching the RPC and the other
  wrappers, instead of Ruby's `write`. The watch call is `watch/3` instead of
  `updates`.
- Timeouts are milliseconds (Erlang's convention), not seconds.
- `SchemaDiff.kind`, permissionship and update operations are atoms, not strings.
- Materialize oneofs are tagged tuples, not structs.
- `read_schema/2`, `computable_permissions/5`, `dependent_relations/5` and
  `diff_schema/4` return `{value, zed_token}` tuples, mirroring Ruby's pairs.
- Streams are lazy `Stream`s, where Ruby relies on grpc-ruby's `ensure`. Elixir
  has no external-iteration gap like Ruby's `Enumerator#next`, but grpc-elixir
  itself gives this client no way to release an abandoned server-streaming
  call; see "Stream lifecycle" above.
- `watch/3` does not wait for a first message before returning, since SpiceDB
  sends nothing until a change.
- Constructors dial eagerly and set `trap_exit` on the caller (grpc-elixir
  behaviour).

## Public API Surface

See the sections above and the `@doc`s on `SpiceDB` for the complete API manifest.

## Examples Manifest

Each example is an ExUnit file at `examples/<name>/<name>_test.exs` that doubles as
an integration test. `examples/support/example_case.ex` resets the server and
writes a shared schema before every test.

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check, conditional results and caveat context |
| `write_relationships/` | Writing relationships with the transaction builder and preconditions |
| `delete_relationships/` | Deleting by filter, guarded deletes with `must_match:`/`must_not_match:`, and paging with `limit:` |
| `read_relationships/` | Reading relationships as a lazy stream across pages |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching from a known revision with a bounded consumer, and a rejected start revision raising on first enumeration |
| `schema_management/` | Schema read and write |
| `bulk_operations/` | Bulk checks, `check_all`, `check_any`, import and export |
| `call_deadlines/` | `default_timeout:`, a per-call `timeout:`, import unbounded by default, and both deadlines biting against a listener that never answers |
| `error_mapping/` | Branching on typed errors and `reason` instead of messages |
| `insecure_opt_in/` | Why `new_plaintext` is loopback-only, and the named opt-in a remote plaintext host requires |
| `retry_policy/` | Which calls are retried and which are not, counted against a stand-in server |
| `unrepresentable_values/` | Caller data that cannot convert fails naming the key; unknown server enums degrade safely |
| `schema_reflection/` | Reflection, computable permissions, dependent relations and diffs |
| `relationship_counters/` | Registering, polling and unregistering a relationship counter |
| `expand_permission_tree/` | Expanding a permission into `PermissionTree` nodes |
| `raw_escape_hatch/` | `proto_client/1` for a proto field and an RPC the idiomatic API does not expose |
| `custom_tls/` | A private CA with `new_custom_tls(ca_cert:)`, and mutual TLS, against a TLS stand-in; tagged `:no_spicedb` |
| `roaring_lookup_resources/` | `experimental_roaring_lookup_resources` against a stand-in, since `authzed/spicedb:latest` answers `UNIMPLEMENTED`; tagged `:no_spicedb` |

`mage integrationTest` starts `docker-compose.test.yml`, runs every example with
`SPICEDB_EXAMPLES=1`, and checks from the JSON report that each one ran.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.

## API compatibility

This client has no API-compatibility gate yet. A breaking change is caught by the
test suite, dialyzer and review, not by a tool, so treat the public surface in
`lib/spicedb.ex` and the public structs as unguarded.

Options follow root DESIGN.md, "RULE: Every RPC wrapper must have one place to add
an option": the trailing keyword list is this client's options container, so a new
option is a new key and never a new positional parameter or a second function.
