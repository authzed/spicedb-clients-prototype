# Migrating to spicedb (Ruby)

This guide covers moving from the official [`authzed`](https://github.com/authzed/authzed-rb)
gem to `spicedb`. Each section lists a behavior change you will observe,
followed by a before/after.

## Requirements

| Dependency | Floor | Notes |
|---|---|---|
| Ruby | >= 3.2 | Ruby 3.4+ moved `logger`, `mutex_m` and `base64` out of the default gems: use Rails >= 7.1, or add those gems explicitly. |
| `grpc` | >= 1.73 | A security floor: older grpc resolves `UNIX://host/...` as a DNS target. |
| `google-protobuf` | >= 3.25 | Works from 3.25 through 4.36. The `authzed` gem's generated files use the old descriptor DSL and fail to load on google-protobuf 4.x (`undefined method 'build' for ... DescriptorPool`), so moving to `spicedb` also unblocks protobuf 4. |
| `googleapis-common-protos-types` | >= 1.11 | Pulled in by grpc. |

`spicedb` and `spicedb-proto` are not published yet; see
[README.md](README.md#installation) for the path-gem setup. The side-by-side
bridge below was verified against `authzed` 1.5.0. One `SpiceDB::Client` per
process is shared by all threads; for Puma, Unicorn and other forking servers
see [README.md, "Concurrency and forking"](README.md#concurrency-and-forking).

## Renamed members

Request and response messages become native values. The common members map
as follows (requests now take keyword arguments on the client method):

| `authzed` (protobuf) | `spicedb` |
|---|---|
| `client.permissions_service.check_permission(CheckPermissionRequest)` | `client.check(PermissionCheck, consistency:)` |
| `check_bulk_permissions(...)` `.pairs[i].item` / `.error` | `client.check_bulk(checks, consistency:)[i]` / `[i].error` (with `item_errors: BulkItemErrors::RETURNED`) |
| `resp.permissionship == :PERMISSIONSHIP_HAS_PERMISSION` | `result.has_permission?` (or `result.permissionship == :has_permission`) |
| `resp.partial_caveat_info.missing_required_context` | `result.missing_context` |
| `resp.checked_at.token` | `result.checked_at.to_s` |
| `WriteRelationshipsResponse#written_at` | `WriteResult#revision` (a `ZedToken`) |
| `ReadRelationshipsResponse#relationship` / `#read_at` / `#after_result_cursor.token` | `ReadRelationship#relationship` / `#read_at` / `#cursor` |
| `ObjectReference(object_type:, object_id:)` | `ObjectRef(object_type:, object_id:)` |
| `SubjectReference(object:, optional_relation:)` (`''` for direct) | `SubjectRef(object:, relation:)` (`nil` for direct) |
| `rel.resource.object_type` / `.object_id` | `rel.resource_type` / `rel.resource_id` (or `rel.resource.object_type`) |
| `rel.subject.object.object_type` / `.object_id` | `rel.subject_type` / `rel.subject_id` |
| `rel.subject.optional_relation` | `rel.subject_relation` |
| `rel.optional_caveat.caveat_name` / `.context` (a `Struct`) | `rel.caveat.name` / `rel.caveat.context` (a `Hash`) |
| `rel.optional_expires_at` (a `Timestamp`) | `rel.expiration` (a `Time`) |
| `RelationshipFilter(optional_resource_id:, optional_relation:, optional_subject_filter:)` | `Filter(resource_id:, relation:, subject: SubjectFilter)` |
| `SubjectFilter(optional_subject_id:, optional_relation:)` | `SubjectFilter(subject_id:, relation: SubjectRelationFilter.direct / .named(name))` |
| `LookupResourcesRequest(resource_object_type:, ...)` | `client.lookup_resources(resource_type:, permission:, subject:, consistency:)` |
| `LookupResourcesResponse#resource_object_id` | `LookupResource#resource_id` |
| `LookupSubjectsRequest(subject_object_type:, optional_subject_relation:)` | `client.lookup_subjects(subject_type:, subject_relation:)` |
| `LookupSubjectsResponse#subject.subject_object_id` | `LookupSubject#subject.subject_id` |
| `ReadSchemaResponse#schema_text` / `#read_at` | `SchemaResult#schema` / `#revision` |
| `WriteSchemaResponse#written_at` | the `ZedToken` `write_schema` returns |
| `ExpandPermissionTreeResponse#tree_root` / `#expanded_at` | `ExpandResult#tree` / `#revision` |
| `DeleteRelationshipsResponse#deleted_at` / `#relationships_deleted_count` / `#deletion_progress` | `DeleteResult#revision` / `#deleted_count` / `#progress` (`:complete`, `:partial`) |
| `Consistency.new(fully_consistent: true)` / `minimize_latency:` / `at_least_as_fresh:` / `at_exact_snapshot:` | `Consistency.full` / `.min_latency` / `.at_least(token)` / `.snapshot(token)` |

Names may be passed as Symbols (`permission: :view`) and object IDs as
Integers (`object_id: user.id`), as the protobuf messages accepted; they are
stored as Strings. Any other type raises `SpiceDB::InvalidArgumentError`
naming the class received.

## Hashes from `to_h`

Code that converted responses with `to_h` and indexed the nested hash keeps
working for native values: `to_h` is deep, and the member names along the
common paths are the protobuf ones.

```ruby
# authzed
item.to_h[:relationship][:subject][:object][:object_id]

# spicedb: the same path, or the short reader
item.to_h[:relationship][:subject][:object][:object_id]
item.subject_id
```

The native shape differs where members were renamed (see the table above):
`[:subject][:relation]` instead of `[:subject][:optional_relation]`,
`[:caveat]` instead of `[:optional_caveat]`, and revisions are `ZedToken`
values (`item.to_h[:read_at].to_s`), not `{ token: ... }` hashes. When the
exact protobuf shape is needed, convert first:

```ruby
SpiceDB::ProtoConv.read_relationship_to_proto(item).to_h
# => { read_at: { token: "..." }, relationship: { resource: {...}, relation: "viewer", subject: {...} }, after_result_cursor: {...} }
```

`ProtoConv` works right after `require 'spicedb'`, without a client.

## Loading both during the migration

`spicedb` uses `spicedb-proto`, the canonical Ruby binding for
`authzed.api.*`, under the same require paths the `authzed` gem uses
(`authzed/api/v1/core_pb`, ...). Ruby's protobuf descriptor pool holds one
definition per proto file, so an unmodified `authzed` gem and `spicedb-proto`
cannot both be loaded: depending on load order you get
`Google::Protobuf::TypeError: duplicate file name authzed/api/v1/core.proto`
or `SpiceDBProto::BindingConflictError`.

To keep `authzed` code running beside `spicedb`, use an `authzed` release (or
a fork) rewritten by `proto-clients/spicedb-ruby-proto/bridge/authzed_bridge.rb`:

```bash
cd proto-clients/spicedb-ruby-proto
bundle exec ruby bridge/authzed_bridge.rb path/to/authzed-rb
```

The bridge deletes the gem's generated `*_pb.rb` files, makes `lib/authzed.rb`
require `spicedb_proto`, and adds the `spicedb-proto` dependency. Afterwards
`Authzed::Api::V1::CheckPermissionRequest` and every other message class is
the same class in both libraries, so values pass between them without
conversion, and `Authzed::Api::V1::Client` keeps working.

## Constructors and transport security

The token is a positional argument instead of an interceptor, TLS is the
default, and plaintext to a non-loopback host needs an explicit opt-in.

```ruby
# authzed
client = Authzed::Api::V1::Client.new(
  target: 'spicedb:50051',
  credentials: :this_channel_is_insecure,
  interceptors: [Authzed::GrpcUtil::BearerToken.new(token: 'key')]
)

# spicedb
client = SpiceDB::Client.new_plaintext('spicedb:50051', 'key',
                                       remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT)
client = SpiceDB::Client.new_system_tls('grpc.authzed.com:443', 'key')
client = SpiceDB::Client.new_custom_tls('spicedb.internal:443', 'key', ca_cert: File.read('ca.pem'))
```

`localhost`, loopback addresses and Unix sockets need no opt-in. The endpoint
may also be an `https://host[:port]` URL (TLS constructors, port 443 by
default) or an `http://host:port` URL (`new_plaintext`, port required). A
plaintext endpoint always needs a port: `new_plaintext('localhost', ...)`,
which `authzed` sent to port 443, now raises; write `localhost:50051`;
`authzed` passed such a URL straight to gRPC, which failed every call.
`dns:///host:port` targets work as they did with `authzed`; the
remote-plaintext check runs on their host. Other
schemes, paths, userinfo and non-numeric ports raise `InvalidArgumentError` at
construction (README.md, "Endpoints"). If you build
your own `GRPC::Core::Channel` (custom channel arguments), wrap it with
`SpiceDB::Client.from_channel(channel, interceptors: [...])`; `close` leaves it
open. `authzed`'s `options:` channel arguments move there.

## Deadlines and retries

`authzed` sets no deadline unless you pass `timeout:`, and never retries.
`spicedb` gives every unary call (including bulk-check chunks) a 30-second
total budget, and retries idempotent reads on UNAVAILABLE and ABORTED up to
three times with jittered backoff. Writes and other mutations are never retried.
Finite paged reads (`read_relationships`, `lookup_resources`,
`lookup_subjects`, `export_relationships`) apply the same default to each page
request, so a server that stalls mid-read raises `DeadlineExceededError`
instead of hanging; a `timeout:` bounds the whole read. Watch and imports have
no default deadline.

```ruby
# authzed
client = Authzed::Api::V1::Client.new(target: t, interceptors: i, timeout: 5)

# spicedb: per call, or client-wide
client.read_schema(timeout: 5)
client = SpiceDB::Client.new_system_tls(t, key, default_timeout: 5)

# keep authzed's behavior (no default deadline, no retries)
client = SpiceDB::Client.new_system_tls(t, key,
                                        default_deadline: SpiceDB::DefaultDeadline::DISABLED,
                                        retries: SpiceDB::Retries::DISABLED,
                                        stream_retries: SpiceDB::StreamRetries::DISABLED)
```

A custom policy is `retry_policy: SpiceDB::RetryPolicy.new(max_retries:, retryable_codes:, initial_backoff:, on_retry:)`.
Every operation also takes a per-call `retry_policy:` that overrides the
client's for that call, including stream establishment: for example
`RetryPolicy.new(max_retries: 0)` on one call, or a policy on a touch-only
`write` (no preconditions) or a `write_schema` to retry those idempotent
writes. Other mutations reject the opt-in. Watch never re-establishes its
stream unless the call passes a policy. To set a policy for every call of one
operation, pass `operation_retry_policies: { watch: SpiceDB::RetryPolicy.new(max_retries: 0) }`
to the constructor (a per-call `retry_policy:` still wins); a `:write` entry
retries only transactions where `txn.idempotent?`.

## Consistency on schema reads

Every read takes an explicit `consistency:`, including `reflect_schema`,
`computable_permissions`, `dependent_relations` and `diff_schema`. Where your
raw requests left consistency unset, pass `SpiceDB::Consistency.min_latency`,
which is what SpiceDB applies when it is absent. (`read_schema` has no
consistency on the wire either.)

## Requests become native values

You no longer build protobuf messages, and every read names its consistency.

```ruby
# authzed
resp = client.permissions_service.check_permission(
  Authzed::Api::V1::CheckPermissionRequest.new(
    consistency: Authzed::Api::V1::Consistency.new(fully_consistent: true),
    resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'readme'),
    permission: 'view',
    subject: Authzed::Api::V1::SubjectReference.new(
      object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
    )
  )
)
allowed = resp.permissionship == :PERMISSIONSHIP_HAS_PERMISSION

# spicedb
alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme'),
                                     permission: 'view', subject: alice)
allowed = client.check(check, consistency: SpiceDB::Consistency.full).has_permission?
```

`check` sends `CheckPermission`; use `check_bulk` for several. Only an
unconditional grant satisfies `has_permission?`; a conditional result reports
`permissionship == :conditional_permission` and `missing_context`.

## Revisions

ZedTokens are `SpiceDB::ZedToken` values, not protobuf messages. Writes return
`SpiceDB::WriteResult`, whose token is `result.revision`.

```ruby
# authzed
token = resp.written_at.token
consistency = Authzed::Api::V1::Consistency.new(at_least_as_fresh: Authzed::Api::V1::ZedToken.new(token: token))

# spicedb
stored = client.write(txn).revision.to_s
consistency = SpiceDB::Consistency.at_least(SpiceDB::ZedToken.new(stored))
```

## Writes, relationships and filters

```ruby
# authzed
client.permissions_service.write_relationships(Authzed::Api::V1::WriteRelationshipsRequest.new(
  updates: [Authzed::Api::V1::RelationshipUpdate.new(operation: :OPERATION_TOUCH, relationship: proto_rel)]
))

# spicedb
txn = SpiceDB::Transaction.new.touch(SpiceDB::Relationship.from_tuple('document:readme#viewer@user:alice'))
client.write(txn)
```

`Relationship.from_tuple` and `#to_s` use SpiceDB's tuple syntax, including
`[caveat:{...}]` and `[expiration:...]`. Field paths become short accessors:
`rel.subject.object.object_id` is also `rel.subject_id` (likewise
`resource_type`, `resource_id`, `subject_type`, `subject_relation`).
Preconditions are `txn.must_match(filter)` / `txn.must_not_match(filter)`.

`read_relationships` and `lookup_resources` page for you and return a stream:
iterate it (or call `to_a`). Read items carry `cursor` and `read_at`; resume
with `cursor:` and the same filter, consistency and `page_size:`. Raw calls
without a limit are one unbounded request; to reproduce one (and its final
cursor), pass `paging: SpiceDB::Paging::SINGLE_REQUEST`.

An empty `Transaction` is sent to SpiceDB like a raw empty write: its
preconditions are checked and the current revision is returned.

## Errors

`authzed` raises raw `GRPC::BadStatus` subclasses. `spicedb` raises
`SpiceDB::Error` subclasses, which are themselves `GRPC::BadStatus`, so
existing handlers keep working:

```ruby
# before (still works)
rescue GRPC::BadStatus => e
  retry_later if e.code == GRPC::Core::StatusCodes::UNAVAILABLE

# after
rescue SpiceDB::FailedPreconditionError
rescue SpiceDB::UnavailableError
```

Rescues of grpc's per-code classes keep catching too: `rescue
GRPC::AlreadyExists` catches `SpiceDB::AlreadyExistsError`, and every grpc
per-code class (`GRPC::FailedPrecondition`, `GRPC::Unavailable`,
`GRPC::Aborted`, ...) matches a native error with its code, in `rescue` and
`case` alike. Ruby has single inheritance, so this is done by extending those
classes' `===`; the native classes are not grpc's subclasses, so
`e.is_a?(GRPC::AlreadyExists)` is false and `e.class` is the `SpiceDB` class.
Code that tests `is_a?`/`kind_of?` on grpc's per-code classes needs the
`SpiceDB::*Error` classes or `e.code` instead.

| grpc class | native class |
|---|---|
| `GRPC::Cancelled` | `SpiceDB::CancelledError` |
| `GRPC::InvalidArgument` | `SpiceDB::InvalidArgumentError` |
| `GRPC::DeadlineExceeded` | `SpiceDB::DeadlineExceededError` |
| `GRPC::NotFound` | `SpiceDB::NotFoundError` |
| `GRPC::AlreadyExists` | `SpiceDB::AlreadyExistsError` |
| `GRPC::PermissionDenied` | `SpiceDB::PermissionDeniedError` |
| `GRPC::ResourceExhausted` | `SpiceDB::ResourceExhaustedError` |
| `GRPC::FailedPrecondition` | `SpiceDB::FailedPreconditionError` |
| `GRPC::OutOfRange` | `SpiceDB::OutOfRangeError` |
| `GRPC::Unavailable` | `SpiceDB::UnavailableError` |
| `GRPC::Unauthenticated` | `SpiceDB::UnauthenticatedError` |
| others (`GRPC::Aborted`, `GRPC::Internal`, ...) | `SpiceDB::Error` with that `code` |

Local errors match by their code as well: local validation is
INVALID_ARGUMENT, so `rescue GRPC::InvalidArgument` also catches a
`SpiceDB::LocalValidationError` (an `InvalidArgumentError`) raised before any
request. `rescue ArgumentError` catches it too, as Ruby code expects around bad
input; a server's INVALID_ARGUMENT is not an `ArgumentError`. A response the
client cannot interpret raises `SpiceDB::Error` with code INTERNAL (grpc's
`GRPC::Internal` matches it). Closing a client while another thread reads one
of its streams raises `SpiceDB::CancelledError` in that reader, never grpc's
own `GRPC::Cancelled`.

`e.server_message` is the server's message verbatim, and always has a value:
for an error raised locally it is the error's message. `e.reason` and
`e.reason_metadata` carry SpiceDB's structured reason. `SpiceDB.code_of(err)`
classifies raw grpc errors too.

## Transport data and protocol output

`authzed` callers that read response metadata with `return_op: true` use
`call_options:` instead:

```ruby
capture = SpiceDB::ResponseMetadata.new
client.read_schema(call_options: SpiceDB::CallOptions.new(metadata: { 'x-request-id' => 'abc' },
                                                          response_metadata: capture))
capture.header
capture.trailer
```

Data SpiceDB versions before 1.30 sent only in trailers is reachable this way;
1.30 is the oldest server supported natively. For protocol-shaped output
(JSON for scripts, backups), use `SpiceDB::ProtoConv` and protobuf's
`to_json`. `client.proto_client` remains as a last-resort escape hatch.

## Testing

`require 'spicedb/testing'` and use `SpiceDB::Testing::Store`: an in-memory
double with every client method, raising the errors the client would. Writes,
reads, deletes, imports, exports, schema reads and writes, checks and both
lookups work in memory. Checks resolve from a result registered with
`set_check_result`, then a resolver callback
(`store.check_resolver = ->(check) { ... }`, or
`SpiceDB::Testing::Store::GRANT_ALL`), then the stored relationships named
like the permission (wildcards included); `lookup_resources` and
`lookup_subjects` return the stored objects that resolution grants. Options are plain keyword arguments, and the store records
every call with them (`store.calls_of(:check).first.options`). Inject failures
with `store.fail_next(:write, SpiceDB::Testing::Store.server_error(code, msg))`
or a `store.fault_hook = ->(fault) { ... }` that sees each call, import batch,
import commit and read/export item. Build expected errors with
`SpiceDB::Error.from_code(GRPC::Core::StatusCodes::FAILED_PRECONDITION, 'msg')`
instead of `GRPC::FailedPrecondition.new`.
