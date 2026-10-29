# spicedb (Ruby)

The idiomatic Ruby client for [SpiceDB](https://authzed.com/spicedb): native
values instead of protobuf messages, explicit consistency, fail-closed checks
and an in-memory test double.

- [MIGRATION.md](MIGRATION.md): moving from the `authzed` gem, with the
  requirements and a map of renamed members.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): concrete patterns for
  checks, writes, reads, errors and tests.
- [DESIGN.md](DESIGN.md): the client's design rules.

## Installation

Requires Ruby >= 3.2, grpc >= 1.73 and google-protobuf >= 3.25 (see
[MIGRATION.md](MIGRATION.md#requirements)). The gems are not published yet;
use them from a checkout of this repository:

```ruby
# Gemfile
gem 'spicedb-proto', path: 'path/to/spicedb-clients/proto-clients/spicedb-ruby-proto'
gem 'spicedb', path: 'path/to/spicedb-clients/spicedb-ruby'
```

## First call

```ruby
require 'spicedb'

client = SpiceDB::Client.new_system_tls('spicedb.example.com:443', ENV.fetch('SPICEDB_TOKEN'))
alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: :user, object_id: 42))
check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: :document, object_id: 'readme'),
                                     permission: :view, subject: alice)
client.check(check, consistency: SpiceDB::Consistency.min_latency).has_permission?
```

Names (types, relations, permissions) may be Strings or Symbols, and object
IDs Strings or Integers; both are stored as Strings.

## Concurrency and forking

**Threads.** One `SpiceDB::Client` is safe for concurrent use from any number
of threads. Build one per process and share it; do not build one per request
or per policy object. Every operation is an independent gRPC call on the
client's one channel, and its own bookkeeping is guarded by a Mutex. A stream
returned by a streaming operation (`read_relationships`, `watch`, ...) belongs
to the thread that iterates it.

**Forks.** The client opens its gRPC channel on first use, in the process that
makes the call. gRPC cannot be used in a child process once its parent has
used it, so:

- A client built before a fork works in every child, as long as the parent
  made **no SpiceDB call** with it (and used no other gRPC client). This is
  the Rails initializer plus Puma `preload_app!` case.
- If the parent did make a call, any call in a child raises
  `SpiceDB::ForkedClientError` (code FAILED_PRECONDITION), explaining the fix,
  instead of hanging or failing inside gRPC.
- Building a fresh client in the child does **not** help once the parent has
  used gRPC (through any client, including `connect!` in the master): gRPC
  refuses all use in that child, and the fresh client raises
  `ForkedClientError` too. The recipe below therefore connects only in the
  workers.
- With grpc's own fork support (`GRPC_ENABLE_FORK_SUPPORT=1`, Linux only, and
  `GRPC.prefork` / `GRPC.postfork_child` / `GRPC.postfork_parent` around each
  fork), a client connected in the parent keeps working in the children.

A Rails initializer builds the shared client, without calling it:

```ruby
# config/initializers/spicedb.rb
SPICEDB = SpiceDB::Client.new_system_tls(ENV.fetch('SPICEDB_ENDPOINT'), ENV.fetch('SPICEDB_TOKEN'))
at_exit { SPICEDB.close }
```

Puma runs anything that must talk to SpiceDB at boot in each worker, never in
the master:

```ruby
# config/puma.rb
workers Integer(ENV.fetch('WEB_CONCURRENCY', 2))
threads 5, 5
preload_app!

on_worker_boot do
  # The first SpiceDB call happens here, in the worker: fail the worker's
  # startup if SpiceDB is unreachable, rather than its first request.
  SPICEDB.connect!(timeout: 10)
end
```

`connect!` is the startup readiness check: it opens the connection in the
calling process and waits until the channel is ready, raising
`SpiceDB::UnavailableError` (unreachable or refusing connections) or
`SpiceDB::DeadlineExceededError` (still connecting) when the timeout passes.
It checks the transport, not the token. Call it in each worker, **never in the
master**: connecting in the master counts as the parent using gRPC, and every
worker's calls would then raise `ForkedClientError`. Without a forking server,
call it once after building the client.

Unicorn's `after_fork` is the same place. With a single-process server (Puma
without `workers`), nothing forks and no hook is needed. Application code then
uses `SPICEDB` from any thread.

## Testing

`require 'spicedb/testing'` provides `SpiceDB::Testing::Store`, an in-memory
double with every client method. It answers checks from registered results, a
resolver callback (`store.check_resolver = SpiceDB::Testing::Store::GRANT_ALL`),
or its stored relationships, and serves `lookup_resources` and
`lookup_subjects`. See [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md#test).

The double does **not** evaluate the schema. A permission such as
`permission view = reader + writer + parent_folder->read` is granted only by a
stored relationship whose relation is literally `view`, or by the check
resolver; storing `document:1#reader@user:alice` does not grant `view`. To
test real permission logic, model it in the resolver, or run SpiceDB itself
(`spicedb serve-testing`, or a container) for schema-accurate results.

## Examples and development

`examples/` doubles as the integration suite (see
[examples/README.md](examples/README.md)). `bundle exec rspec` runs the unit
specs and `bundle exec rubocop` lints.
