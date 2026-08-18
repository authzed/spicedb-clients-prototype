# spicedb-ruby — Idiomatic Ruby Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Ruby-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default. The API should feel like
hand-written, idiomatic Ruby — not a thin wrapper around gRPC.

### Gem & Module

- **Gem name**: `spicedb`
- **Top-level module**: `SpiceDB`
- **Minimum Ruby**: 3.2 (for `Data.define`)

### Module Structure

Six modules under `SpiceDB`:

- **`SpiceDB::Client`** — the client class and all SpiceDB operations
- **`SpiceDB::Consistency`** — module with factory methods for consistency modes
- **`SpiceDB::Relationship`** — `Data.define` value type for relationships
- **`SpiceDB::Filter`** — `Data.define` value type for relationship filters
- **`SpiceDB::Transaction`** — builder for batching relationship writes
- **`SpiceDB::Errors`** — exception hierarchy and gRPC error mapping

Types (`Relationship`, `Filter`, `Consistency`) are independent of `Client`.
Users can construct relationships and filters without requiring the client.

### Constructors

Security-obvious named constructors:

- `SpiceDB::Client.new_plaintext(endpoint, token)` — for testing, makes
  insecure connection obvious
- `SpiceDB::Client.new_system_tls(endpoint, token)` — for production
- Block form: `SpiceDB::Client.new_plaintext(...) { |client| ... }` — yields
  client and ensures cleanup

### Consistency

ZedTokens are opaque `String` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Module methods in `SpiceDB::Consistency`:
- `full` — fully consistent, least performant
- `min_latency` — SpiceDB's preferred revision, optimal performance
- `at_least(revision)` — read-after-write
- `snapshot(revision)` — exact revision
- `at_least_or_full(revision)` — AtLeast if revision present, Full otherwise
- `at_least_or_min_latency(revision)` — AtLeast if revision present, MinLatency otherwise

All write operations return a revision string.

### Relationships

Immutable `Data.define` value type:

```ruby
SpiceDB::Relationship = Data.define(
  :resource_type, :resource_id, :resource_relation,
  :subject_type, :subject_id, :subject_relation,
  :caveat_name, :caveat_context, :expiration,
  :check_context
)
```

Constructors:
- `SpiceDB::Relationship.new(...)` — standard Data.define constructor
- `SpiceDB::Relationship.from_triple(...)` — from resource/subject triples
- `SpiceDB::Relationship.from_tuple(string)` — parse tuple string format

Immutable modifiers (return new instances):
- `r.with_caveat(name, context)` — returns new relationship with caveat
- `r.with_expiration(time)` — returns new relationship with expiration
- `r.with_check_context(context)` — returns new relationship with check-time
  caveat context (see Checks below) — distinct from `with_caveat`'s
  write-time context
- `r.to_filter` — returns a Filter matching this relationship's resource

`check_context` is check-time-only caveat context for the check surface — a
**different concept** from `caveat_context`, which is write-time context
embedded in `optional_caveat` and persisted to SpiceDB. `check_context` has
no wire representation on the write path at all; it is read exclusively by
`check_permission`/`check_permissions` (see Checks below). Conflating the
two would leak check-time-only context into a write, silently altering a
stored relationship's evaluated caveat forever — they are kept independently
settable (`with_caveat` and `with_check_context` never touch each other's
field) for exactly that reason.

**Caveat context types.** Caveat context crosses the wire as
`google.protobuf.Struct`, whose values are a `kind` oneof. Conversion must dispatch on
that oneof in both directions; **today only the read direction does.**
`relationship_from_proto` dispatches on `kind`, so context already stored in SpiceDB
reads back with its types intact. `relationship_to_proto` still stringifies every value
(`Value.new(string_value: v.to_s)`) — a known defect it shares with the Go, C# and Java
clients, corrected separately in the cross-client write-path work. Until that lands,
context written *through this client* is stored as strings:
`with_caveat("x", { "n" => 42 })` reads back the String `"42"` — the read path
faithfully reporting what is on the wire, not a read bug.

Reading a non-string value via `Value#string_value` returns `""`, silently destroying
stored context rather than raising — which is why the `kind` dispatch is required for
correctness and not merely tidiness. `Struct#fields` is a `Google::Protobuf::Map`,
**not** a `Hash` — Hash-only methods such as `transform_values` raise `NoMethodError`
on it directly. `Map#to_h` is not a safe
substitute either: for message-valued maps it recursively converts each `Value` via the
generic protobuf-to-hash conversion rather than leaving it as a `Value` to dispatch on.
`Map` includes `Enumerable`, so conversion iterates its raw entries directly (e.g. via
`each_with_object`) instead of going through `Hash` or `to_h` at all.

A numeric caveat context value reads back as a `Float` (`42` becomes `42.0`), because
`google.protobuf.Value.number_value` is a `double`. This applies to any value that
reached the wire *as a number* — written by another client, by `zed`, or by this client
once its write path is corrected — and so it will apply to a Ruby `Integer` generally
at that point; today an `Integer` written through `with_caveat` comes back as a
`String` instead, per the paragraph above. The `Float` widening is inherent to the
proto and consistent across all seven clients; it is not worked around.

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `check_permission(consistency, permission, relationship, context: nil)` → `CheckResult`
- `check_permissions(consistency, permission, *relationships, context: nil)` → `Array<CheckResult>`
- `check_any(consistency, permission, *relationships, context: nil)` → `Boolean`
- `check_all(consistency, permission, *relationships, context: nil)` → `Boolean`

`check_permission`/`check_permissions` return `SpiceDB::CheckResult`, not a
bare `Boolean` — `CheckPermissionResponse#permissionship` is four-valued
(`UNSPECIFIED`/`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`), and
a caveated relationship whose context wasn't supplied at check time comes
back `CONDITIONAL_PERMISSION` — the server saying "I need more information,"
which collapsing to a Boolean would silently turn into either a grant or a
denial.

```ruby
SpiceDB::CheckResult = Data.define(:permissionship, :missing_context, :checked_at) do
  def has_permission?
    permissionship == :has_permission
  end
end
```

- `permissionship` — a Symbol: `:unspecified`, `:no_permission`,
  `:has_permission`, or `:conditional_permission` (the same native symbol
  set the lookup surfaces use, below — checks are simply the one surface
  that can affirmatively produce `:no_permission`)
- `missing_context` — `Array<String>` of caveat parameter names SpiceDB
  couldn't evaluate because context wasn't supplied; `[]` unless
  `permissionship` is `:conditional_permission`
- `checked_at` — the ZedToken (`String`) the check was evaluated against
- `has_permission?` — `true` ONLY when `permissionship` is
  `:has_permission`

**Callers MUST call `result.has_permission?` — never test the result
itself.** Ruby has no `__bool__`-style hook: every `CheckResult` is truthy in
a bare `if result` regardless of `permissionship`, so `if result` is
unconditionally true even for a `:conditional_permission` result. This is
the one mitigation available in Ruby for the truthiness hazard that a
Boolean-returning check API creates when it's replaced by an object — unlike
Python (`__bool__`), Ruby cannot make the object itself refuse to be truthy.

`check_any`/`check_all` remain plain `Boolean` — they count ONLY
`:has_permission` results. A `:conditional_permission` result does NOT
count as a grant for either (deliberately fail-closed): `check_any` is
`results.any?(&:has_permission?)` and `check_all` is
`results.all?(&:has_permission?)`.

#### Supplying caveat context

`:conditional_permission` alone is not actionable — the caller also needs a
way to supply the caveat context named in `missing_context` and get back an
actual grant/denial. All four check methods accept an optional `context:`
keyword, and `SpiceDB::Relationship` carries a matching `check_context`
field, so a caller can supply context two ways:

- **Call-level** — `context:` on `check_permission`/`check_permissions`/
  `check_any`/`check_all` is a default applied to every relationship in the
  call.
- **Per-item** — `relationship.with_check_context({...})` (or
  `Relationship.new(..., check_context: {...})`) overrides `context:` for
  that one relationship's check.

```ruby
# Call-level: applies to every relationship checked in this call.
client.check_permissions(consistency, "view", rel1, rel2, context: { now: 42 })

# Per-item: overrides the call-level default for just this relationship.
rel = SpiceDB::Relationship.from_triple("doc", "1", "viewer", "user", "alice")
                            .with_check_context({ now: 42 })
client.check_permission(consistency, "view", rel)
```

All checks go through `BulkCheckPermissions` under the hood, whose wire
format (`CheckBulkPermissionsRequestItem#context`, proto field 4) attaches
context **per item** — `CheckBulkPermissionsRequest` itself has no context
field. So `context:` is fanned out onto every item at request-build time,
and each item's own `check_context` (if any) is then merged on top:

**Merge rule: key-level, item wins.** `call_level.merge(item_level)` — an
item's own keys override the call-level default on conflict, but call-level
keys the item doesn't mention are **retained**, not dropped. An item with no
`check_context` inherits `context:` unchanged. This is deliberately NOT a
wholesale replacement: if a single per-item key silently discarded every
other call-level key, a caveat would fail for context the caller believed it
had already supplied, landing right back in the confusing
`CONDITIONAL_PERMISSION` state this feature exists to make legible.

```
call-level:  { now: 42, region: "us" }
item-level:  { region: "eu" }
sent for that item: { now: 42, region: "eu" }
```

If neither call-level nor per-item context is supplied for an item, no
`context` field is set on the wire at all (nil, not an empty `Struct`).

This is purely additive — `context:` defaults to `nil` on every check
method and `check_context` defaults to `nil` on `Relationship`, so no
existing call site changes.

### Lookups

`lookup_resources`/`lookup_subjects` yield native result objects — never
bare ID strings — so callers can't accidentally treat a caveated or
wildcard-excluded result as a full grant. Mirrors spicedb-go's
`client/lookup_types.go`.

```ruby
SpiceDB::PartialCaveatInfo = Data.define(:missing_required_context)
SpiceDB::LookupResource    = Data.define(:resource_id, :permissionship, :partial_caveat, :looked_up_at)
SpiceDB::ResolvedSubject   = Data.define(:subject_id, :permissionship, :partial_caveat)
SpiceDB::LookupSubject     = Data.define(:subject, :excluded_subjects, :looked_up_at)
```

`permissionship` is a Symbol: `:unspecified`, `:has_permission`, or
`:conditional_permission` (lookups never produce `:no_permission` — see
Checks above for the fourth value). `partial_caveat` is `nil` unless
`permissionship` is `:conditional_permission`, in which case it carries the
`missing_required_context` that must be supplied to fully evaluate the
grant. `looked_up_at` is the ZedToken (`String`) the lookup was evaluated
against — pass it to a later `Consistency.at_least` for read-your-writes
against this result.

- `lookup_resources(...)` → `Enumerator<SpiceDB::LookupResource>`
- `lookup_subjects(...)` → `Enumerator<SpiceDB::LookupSubject>`, where
  `LookupSubject#subject` is a `ResolvedSubject` and
  `LookupSubject#excluded_subjects` is an `Array<ResolvedSubject>`

Callers MUST check `permissionship` before treating a `LookupResource` as a
full grant. For `lookup_subjects`, when `subject.subject_id` is the
wildcard `"*"`, callers MUST check `excluded_subjects` before treating the
wildcard as a blanket grant — the server grants the permission to every
subject of the requested type EXCEPT those listed there.

### Streaming & Transparent Cursor Pagination

Ruby `Enumerator` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single Enumerator, and the client transparently re-fetches
pages using the cursor from each response. Default page sizes use sensible
defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `read_relationships` | 512 | cursor-based auto-pagination |
| `lookup_resources` | 512 | cursor-based auto-pagination |
| `lookup_subjects` | — | single streaming call |
| `export_relationships` | 512 | cursor-based auto-pagination |
| `delete_relationships` | 1,000 | auto-repeats until all deleted; matches SpiceDB's default `--max-delete-relationships-limit` |
| `import_relationships` | 1,000 | batches into streaming sends |
| `updates` | — | server-streaming, no pagination |

Enumerators support `.lazy` for memory-efficient processing of large result sets.

### Writes

Transaction builder pattern:

```ruby
txn = SpiceDB::Transaction.new
txn.create(relationship)
txn.touch(relationship)
txn.delete(relationship)
txn.must_not_match(filter)  # precondition
txn.must_match(filter)      # precondition
revision = client.write(txn)
```

### Deletions

`delete_relationships` automatically pages through large result sets using a
limit of 1,000 per RPC call (override with `limit:`; matches SpiceDB's
default `--max-delete-relationships-limit`, so the default works against a
stock server). It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

Optional `must_match:`/`must_not_match:` keyword args add preconditions that
guard the delete, mirroring `Transaction#must_match`/`#must_not_match`:

```ruby
client.delete_relationships(filter, must_match: [guard_filter])
client.delete_relationships(filter, must_not_match: [guard_filter], limit: 500)
```

Preconditions are a per-request proto field, so a delete spanning multiple
auto-paged calls re-evaluates them on every page rather than checking once
for the whole operation — pair a precondition with a `limit:` large enough
to cover every match in one call for all-or-nothing semantics.

### Error Handling

Exception hierarchy under `SpiceDB::Error`:
- `SpiceDB::PermissionDeniedError`
- `SpiceDB::NotFoundError`
- `SpiceDB::AlreadyExistsError`
- `SpiceDB::InvalidArgumentError`
- `SpiceDB::FailedPreconditionError`
- `SpiceDB::UnavailableError`
- `SpiceDB::CancelledError`
- `SpiceDB::DeadlineExceededError`
- `SpiceDB::ResourceExhaustedError`

Automatic retry with exponential backoff for transient gRPC errors
(UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED).

### Complete Method List

**Checks:**
- `check_permission(consistency, permission, relationship, context: nil)` → `CheckResult`
- `check_permissions(consistency, permission, *relationships, context: nil)` → `Array<CheckResult>`
- `check_any(consistency, permission, *relationships, context: nil)` → `Boolean`
- `check_all(consistency, permission, *relationships, context: nil)` → `Boolean`

**Relationships:**
- `write(transaction)` → `String` (revision)
- `read_relationships(consistency, filter)` → `Enumerator<Relationship>`
- `delete_relationships(filter, must_match: [], must_not_match: [], limit: nil)` → `String` (revision)

**Lookups:**
- `lookup_resources(consistency, resource_type, permission, subject_type, subject_id)` → `Enumerator<LookupResource>`
- `lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)` → `Enumerator<LookupSubject>`

**Schema:**
- `read_schema` → `[String, String]` (schema, revision)
- `write_schema(schema)` → `String` (revision)
- `reflect_schema(consistency)` → `ReflectSchemaResult`
- `computable_permissions(consistency, definition_name, relation_name)` → `[Array<RelationReference>, String]`
- `dependent_relations(consistency, definition_name, permission_name)` → `[Array<RelationReference>, String]`
- `diff_schema(consistency, comparison_schema)` → `[Array<SchemaDiff>, String]`

**Expand:**
- `expand_permission_tree(consistency, resource_type, resource_id, permission)` → `ExpandResult`

**Bulk:**
- `import_relationships(enum)` → `Integer` (num_loaded)
- `export_relationships(consistency, filter = nil)` → `Enumerator<Relationship>`

**Watch:**
- `updates(object_types, start_revision: nil)` → `Enumerator<Update>`

**Experimental:**
- `experimental_register_relationship_counter(name, filter)` → `nil`
- `experimental_count_relationships(name)` → `CountResult`
- `experimental_unregister_relationship_counter(name)` → `nil`

### Escape Hatches

The proto client (`spicedb-proto` gem) is accessible via `client.proto_client`
for advanced use cases that need direct access to gRPC stubs.

## Public API Surface

See module sections above for the complete API manifest.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
