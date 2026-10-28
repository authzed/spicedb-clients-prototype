# Migrating to spicedb (Ruby)

This guide covers moving from the official [`authzed`](https://github.com/authzed/authzed-rb)
gem to `spicedb`. Each section lists a behavior change you will observe,
followed by a before/after.

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

`localhost`, loopback addresses and Unix sockets need no opt-in. If you build
your own `GRPC::Core::Channel` (custom channel arguments), wrap it with
`SpiceDB::Client.from_channel(channel, interceptors: [...])`; `close` leaves it
open. `authzed`'s `options:` channel arguments move there.

## Deadlines and retries

`authzed` sets no deadline unless you pass `timeout:`, and never retries.
`spicedb` gives every unary call (including bulk-check chunks and paged reads)
a 30-second total budget, and retries idempotent reads on UNAVAILABLE and
ABORTED up to three times with jittered backoff. Writes and other mutations are
never retried. Streams have no default deadline.

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

Rescues of grpc's per-code classes (`rescue GRPC::FailedPrecondition`) do
**not** catch native errors; switch them to the `SpiceDB::*Error` classes or
compare `e.code`. `e.server_message` is the server's message verbatim;
`e.reason` and `e.reason_metadata` carry SpiceDB's structured reason.
`SpiceDB.code_of(err)` classifies raw grpc errors too.

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
double with the client's `write`, `read_relationships`, `delete_relationships`,
`import_relationships`, `export_relationships`, `read_schema`, `write_schema`,
`reflect_schema`, `check` and `check_bulk` signatures, raising the errors the
client would. Options are plain keyword arguments, and the store records
every call with them (`store.calls_of(:check).first.options`). Inject failures
with `store.fail_next(:write, SpiceDB::Testing::Store.server_error(code, msg))`
or a `store.fault_hook = ->(fault) { ... }` that sees each call, import batch,
import commit and read/export item. Build expected errors with
`SpiceDB::Error.from_code(GRPC::Core::StatusCodes::FAILED_PRECONDITION, 'msg')`
instead of `GRPC::FailedPrecondition.new`.
