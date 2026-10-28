# spicedb (Ruby): integration guide for AI coding agents

Concrete Ruby for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Ruby has no typed (generated) client, so all code
uses the schema-agnostic native client.

| Gem / require | Constant | Role |
|---|---|---|
| `spicedb` / `require 'spicedb'` | `SpiceDB::Client` | native client, options, errors, results |
| `spicedb` | `SpiceDB::ObjectRef`, `SubjectRef`, `Relationship`, `Filter`, `Transaction` | values and the write builder |
| `spicedb` | `SpiceDB::Consistency`, `SpiceDB::ZedToken` | strategies and revisions |
| `spicedb` / `require 'spicedb/testing'` | `SpiceDB::Testing::Store` | in-memory test double |
| `spicedb` | `SpiceDB::ProtoConv` | protocol conversions (tools only) |
| `spicedb-proto` | `SpiceDBProto`, `Authzed::Api::V1::*` | canonical protocol bindings; do not build requests with them |

Do **not** load the `authzed` gem in the same process unless it has been
bridged onto `spicedb-proto` (`bridge/authzed_bridge.rb`; see
[MIGRATION.md](MIGRATION.md)). An unbridged copy raises
`SpiceDBProto::BindingConflictError` at load time.

## Connect once

```ruby
client = SpiceDB::Client.new_system_tls('spicedb.example.com:443', ENV.fetch('SPICEDB_TOKEN'))
at_exit { client.close }
```

`new_plaintext` is for local development; a non-loopback plaintext target also
needs `remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT`.
`new_custom_tls(endpoint, token, ca_cert:)` trusts a private CA. To use an
existing `GRPC::Core::Channel`, wrap it; `close` leaves the channel open:

```ruby
client = SpiceDB::Client.from_channel(channel, interceptors: [SpiceDBProto::BearerTokenInterceptor.new(token)])
```

Every constructor yields and closes the client when given a block.

## Check (fail closed)

```ruby
alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: user_id))
org = SpiceDB::ObjectRef.new(object_type: 'organization', object_id: org_id)
check = SpiceDB::PermissionCheck.new(resource: org, permission: 'view', subject: alice)
begin
  allowed = client.check(check, consistency: SpiceDB::Consistency.min_latency).has_permission?
rescue SpiceDB::Error
  allowed = false # errors and conditional results both deny
end
raise Forbidden unless allowed
```

- Never use the result's truthiness; only `has_permission?` grants.
- Need to observe a write? Store `written.revision.to_s` and check with
  `SpiceDB::Consistency.at_least(SpiceDB::ZedToken.new(stored))`.
- Several at once (results are in input order; each item's revision is its chunk's):

```ruby
checks = %w[a b].map do |id|
  SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'organization', object_id: id),
                               permission: 'view', subject: alice)
end
granted = client.check_bulk(checks, consistency: SpiceDB::Consistency.min_latency).map(&:has_permission?)
```

  With `item_errors: SpiceDB::BulkItemErrors::RETURNED`, a failed item carries
  `result.error` instead of failing the call; it never grants.
- Permission names chosen at runtime are plain strings here. There is no
  generated name list, so verify them at startup with
  `client.reflect_schema(consistency: SpiceDB::Consistency.full)`.

## List what a subject can access

```ruby
ids = client.lookup_resources(resource_type: 'organization', permission: 'view', subject: alice,
                              consistency: SpiceDB::Consistency.min_latency,
                              permissionship_filter: SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY)
            .map(&:resource_id)
```

Omit the filter only if you handle `:conditional_permission` results explicitly.

## Write atomically

```ruby
admins = SpiceDB::Filter.new(resource_type: 'organization', resource_id: org_id, relation: 'admin')
other_admin = admins.with_subject(SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: other_admin_id))
txn = SpiceDB::Transaction.new
                          .must_match(other_admin) # invariant: another admin remains
                          .delete(SpiceDB::Relationship.from_tuple("organization:#{org_id}#admin@user:#{user_id}"))
                          .touch(SpiceDB::Relationship.from_tuple("organization:#{org_id}#member@user:#{user_id}"))
begin
  written = client.write(txn)
rescue SpiceDB::FailedPreconditionError
  raise LastAdmin
end
```

- `touch` upserts; `create` raises `SpiceDB::AlreadyExistsError` if present.
- Deletes match by tuple identity: build the relationship without its caveat
  or expiration (`from_tuple("invitation:#{id}#inviter@user:#{u}")`).
- Delete what you just read: `txn.delete(item)` accepts a `ReadRelationship`.
- **Do not retry a failed write blindly**; it may have committed. The client
  never retries mutations by default. A touch-only write without
  preconditions (and `write_schema`) is safe to replay, so one call may opt in:
  `client.write(txn, retry_policy: SpiceDB::RetryPolicy.new)`. Any other
  mutation raises `SpiceDB::InvalidArgumentError` for that opt-in.
- An empty transaction is sent: SpiceDB checks its preconditions and returns
  the current revision.

## Read relationships

```ruby
admin_ids = client.read_relationships(admins, consistency: SpiceDB::Consistency.min_latency, page_size: 500)
                  .map(&:subject_id)
```

Each item also carries `item.cursor` and `item.read_at`. To resume, pass
`cursor: item.cursor` with the **same filter, consistency and page size**.
Results stream: iterate with a block or `each`; `break` releases the RPC.
`paging: SpiceDB::Paging::SINGLE_REQUEST` (reads and resource lookups) sends
exactly one request like the raw API: its limit is `limit:` (unbounded
without one) and `page_size:` is ignored.

## Schema-agnostic and tooling code

```ruby
relationship = SpiceDB::Relationship.from_tuple('document:readme#viewer@user:alice[expiration:2030-01-01T00:00:00Z]')
written = client.write(SpiceDB::Transaction.new.touch(relationship))
result = client.check(SpiceDB::PermissionCheck.new(resource: relationship.resource, permission: 'view',
                                                   subject: relationship.subject),
                      consistency: SpiceDB::Consistency.at_least(written.revision))
```

`from_tuple` and `to_s` use SpiceDB's full syntax, including `[caveat:{...}]`
and `[expiration:...]`. Tools with their own policies:

```ruby
tool = SpiceDB::Client.new_plaintext('localhost:50051', token,
                                     retries: SpiceDB::Retries::DISABLED, # or retry_policy: SpiceDB::RetryPolicy.new(...)
                                     default_deadline: SpiceDB::DefaultDeadline::DISABLED,
                                     stream_retries: SpiceDB::StreamRetries::DISABLED)
capture = SpiceDB::ResponseMetadata.new
tool.read_schema(call_options: SpiceDB::CallOptions.new(response_metadata: capture))
capture.trailer # e.g. io.spicedb.respmeta.* and x-request-id
```

Every operation accepts `call_options:` and a per-call `retry_policy:`
that overrides the client's for that call, including stream establishment
(`retry_policy: SpiceDB::RetryPolicy.new(max_retries: 0)` exempts one call).
`operation_retry_policies: { watch: SpiceDB::RetryPolicy.new(max_retries: 0) }`
on the constructor sets one operation's policy for every call (a per-call
policy still wins); a `:write` entry retries only `txn.idempotent?` writes,
and deletes, imports and counter registration reject one with retries.
Protocol-shaped output: `SpiceDB::ProtoConv.relationship_to_proto(relationship).to_json`;
`ProtoConv.check_bulk_to_proto(checks, results)` rebuilds a whole bulk-check
response, per-item errors included.
`import_relationships(rels, batch_size:)` is one transaction per call;
`delete_all_relationships_non_atomic(filter, on_progress:)` reports each batch.

## Errors

```ruby
begin
  client.write(txn)
rescue SpiceDB::FailedPreconditionError # precondition failed
rescue SpiceDB::OutOfRangeError          # stale ZedToken: drop it, re-read fully consistent
rescue SpiceDB::UnavailableError         # transient; reads were already retried
end
```

Every `SpiceDB::Error` is a `GRPC::BadStatus`, so `rescue GRPC::BadStatus => e`
and `e.code == GRPC::Core::StatusCodes::FAILED_PRECONDITION` also work.
`SpiceDB.code_of(err)` classifies any error, including a raw
`GRPC::BadStatus`. Forward `e.server_message`, not `e.message`, when relaying
an error to another party.

## Test

```ruby
require 'spicedb/testing'

store = SpiceDB::Testing::Store.new([SpiceDB::Relationship.from_tuple('organization:acme#admin@user:alice')])
store.set_check_result(SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'organization', object_id: 'acme'),
                                                    permission: 'view', subject: alice), :has_permission)
expected = SpiceDB::Error.from_code(GRPC::Core::StatusCodes::FAILED_PRECONDITION, 'precondition failed')
```

Inject the client into your code; `SpiceDB::Testing::Store` has the same
`write`, `read_relationships`, `delete_relationships`, `import_relationships`,
`export_relationships`, `read_schema`, `write_schema`, `reflect_schema`,
`check` and `check_bulk` parameters and results, applying preconditions,
create conflicts, cursors, limits and filter semantics (`Filter#matches?`).
Its errors have the client's classes, codes and messages (the server's message
for server failures). `set_reflect_schema_result` and `set_response_metadata`
register what reflection and `call_options:` capture return. `store.calls_of(:write)`
returns recorded calls (`request`, keyword `options`); `store.fail_next(:check, err)`
fails one call, and `store.fault_hook = ->(fault) { ... }` can fail an import batch
(`fault.stage == SpiceDB::Testing::FaultStage::IMPORT_BATCH`, `fault.item`) or a
read/export after some items (`FaultStage::ITEM`); return
`SpiceDB::Testing::Store.server_error(GRPC::Core::StatusCodes::UNAVAILABLE, 'down')`
from the hook. For integration tests, run
`authzed/spicedb serve-testing` and assert both granted and denied cases.
