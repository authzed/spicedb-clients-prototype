# Changelog

## Unreleased

### Fixed

- **2026-08-19**: **Six examples ran (or, for `watch_changes/`, did not run) without being able
  to fail.** Root DESIGN.md, "RULE: An example must be executed by CI and must be able to fail",
  clause 2. No example was renamed or removed.

  - `examples/watch_changes/` **now runs in CI**. It was skipped by name -- "open-ended stream;
    needs a bounded consumer with explicit cancellation" -- so the only streaming example never
    executed and "RULE: Abandoning a stream must release it" had no executed coverage here at
    all. It is now a bounded consumer: subscribe from a known revision, write the update it is
    waiting for, consume until exactly that update arrives, then `break` (which is this client's
    abandonment path -- see DESIGN.md, "Stream lifecycle: abandoning an Enumerator releases it").
    Both examples run under `Timeout.timeout`, so a stalled stream fails with a message instead
    of hanging the job.
  - `examples/relationship_counters/` polls to a terminal state instead of sleeping two seconds
    and then wrapping every assertion in `unless result.still_calculating`, which asserts nothing
    on a slow run and nothing on any run if that mapping is inverted. The count asserted is exact
    (two viewers, with an `editor` written that the filter must exclude), and unregistering is
    verified by requiring the subsequent read to raise. The second example's
    `expect([true, false]).to include(result.still_calculating)` -- which passes for anything
    except `nil` -- is gone.
  - `examples/call_deadlines/` gains two examples against a `TCPServer` that accepts connections
    and never speaks gRPC, requiring `SpiceDB::DeadlineExceededError` from both `default_timeout:`
    and a per-call `timeout:`. Each runs under a watchdog, so a client that accepted the keyword
    and never attached it fails the example rather than hanging the job.
  - `examples/delete_relationships/` reads every delete back. It asserted only that a revision
    came back, which happens whether or not anything was deleted -- so `must_match:` and `limit:`
    being dropped entirely would have passed, which is every claim each example's own title
    makes. The rejected guarded delete is now also required to have deleted nothing, and
    `limit: 1` is exercised against three owners so a paging loop that stopped after one page
    fails.
  - `examples/schema_reflection/` asserts contents. `expect(diffs.map(&:kind)).not_to be_empty`
    stood two lines after asserting `diffs` itself is non-empty -- `map` preserves length, so it
    could not fail; the specific diff kinds are asserted now, and `:unknown` is rejected.
    Relations and permissions are compared as exact sets rather than "not empty".
  - `examples/raw_escape_hatch/` reads `optional_transaction_metadata` back out of the Watch
    stream. It was sent and never verified -- and since that field is not on the idiomatic
    `SpiceDB::WatchEvent` either, reading it back is a second use of the same hatch, which is
    exactly the point the example is making.

- **2026-08-19**: **`examples/spec_helper.rb`'s shared hook was not clear-before-write.** It wrote
  `TEST_SCHEMA` and *then* deleted, which is safe only because `TEST_SCHEMA` is a superset that
  drops no relation -- narrow it at any point and this gem acquires the exact defect the other
  four clients just fixed, with no warning. Two rounds of review documents described this hook as
  the model the others should copy, which it was not. It now deletes first and writes second,
  tolerating only `FailedPreconditionError` (a fresh server with no `document` definition).

- **2026-08-19** (output only): the integration summary read `N test cases (M skipped)`, where the
  parenthesised number counts skipped *example directories* and sat immediately after a *case*
  count. Now that skipped cases are a separately tracked concept, it reads `N test cases executed;
  M examples skipped`.

- **2026-08-19**: **The example set is pinned by name, not by count.** `wantExampleCount` passed
  unchanged when an example directory was *renamed* -- only deletion was caught, and a manifest
  can drift from disk with no signal. `wantExamples` now lists every example by name and is
  reconciled with the glob in both directions, the same shape the skip targets already used.
  Verified by renaming `examples/lookup_subjects`: `expected but absent: [lookup_subjects];
  present but not expected: [lookup_subj]`.

- **2026-08-19**: **A skipped case no longer counts as an executed one.** The report reader
  behind the executed-count assertion counted every reported case, so a fully-`xit`/`skip` example
  would have satisfied "this example contributed a test case" while running nothing. Skipped
  cases are now excluded and the reported total is the executed total. No such example exists
  today; the point is that adding one cannot go unnoticed.

- **2026-08-19** (documentation only): **Corrected the causal claim about `--tag ~watch`.**
  `examples/README.md` and `watch_changes_spec.rb` said a tag filter "that matches nothing exits 0,
  which is how this spec came to be tracked ... and never once executed". That is two true facts
  glued into one false claim: the filter *did* match -- the spec carried `:watch` on both blocks --
  and excluded it, because the flag predates the file. A filter matching nothing is a separate
  hazard, and the one the JSON report now covers. Both texts now say which is which, matching what
  `Magefile.go` already said. The now-redundant `:watch` tags are removed, so a reintroduced
  `--tag ~watch` cannot silently double-exclude.

- **2026-08-19**: **`rspec --tag ~watch` replaced by a named, counted skip list.**
  (Corrected by the entry above: the opening clause here originally blamed a filter that matches
  nothing, which is not what happened.) `examples/watch_changes/watch_changes_spec.rb` was
  git-tracked, listed in `examples/README.md` with no caveat, and **never once executed in CI**,
  because `--tag ~watch` has been in the Magefile since the first Ruby commit and the spec was
  added later already carrying `:watch` on both `it` blocks -- so the filter matched it and
  excluded it from the day it landed. A filter matching *nothing* is a separate hazard, covered
  by the JSON report below. `IntegrationTest` now names
  the example directories on the rspec command line, skips `watch_changes/` by an explicit named
  entry that prints its reason, and then reads rspec's JSON report to confirm every selected
  example actually contributed a spec. The selection is unchanged: 49 rspec examples, verified
  identical to the old filter's by diffing `--dry-run --format json` output. New
  `mage checkExamples` (also run by `mage test`, so it needs no server) asserts
  `examples/*/*_spec.rb` still matches the expected count and that every skipped name is an
  example that exists. Root DESIGN.md, "RULE: An example must be executed by CI and must be able
  to fail", clause 1.

  `.github/workflows/rust.yaml` already greps its test output for `"1 passed"` for exactly this
  reason; this is that guard, applied to the runner where the defect had actually landed.

- **2026-08-19** (documentation only): **`examples/README.md` documented a command that runs
  nothing and reports success.** It said `cd examples && bundle exec rspec`. With no `.rspec`
  present, RSpec falls back to its default path `spec`, which does not exist inside `examples/`,
  so that command loads zero files, prints "No examples found", and exits 0 -- the primary
  documented way to run the Ruby examples was green over nothing. The README now runs rspec from
  the gem root with the path named (`bundle exec rspec examples/`), explains why the path
  matters, and names `mage integrationTest` as the one command that starts the container. It also
  documented `SPICEDB_TOKEN=testtoken`, which is not the key `docker-compose.test.yml` starts
  SpiceDB with, so following it verbatim produced `UNAUTHENTICATED`; the default is
  `somerandomkeyhere`, which is what `examples/spec_helper.rb` has always used.

- **2026-08-19**: **A large bulk check is no longer sent as one oversized request.**
  `#check_permissions`, `#check_permission`, `#check_any` and `#check_all` built a single
  `CheckBulkPermissions` request from however many relationships the caller passed. SpiceDB caps
  a request at `maxBulkCheckCount` -- 10,000, a hard-coded const in
  `internal/services/v1/bulkcheck.go` with no flag to raise or lower it -- and rejects anything
  larger with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto enforced the cap
  either (`CheckBulkPermissionsRequest.items` carries only a per-item `required` rule, not a
  collection-size rule), so the failure surfaced only at runtime, on the largest inputs.

  Checks are now split into requests of at most 1,000 items -- the same batch size the import
  path already uses, and the value `spicedb-rust` (the one client that already chunked) picked
  -- and the responses are concatenated in input order, so `results[i]` still corresponds to the
  caller's i-th relationship across a chunk boundary. The response-length guard added earlier on
  this branch now runs per chunk. A caller passing fewer than 1,000 relationships still makes
  exactly one request.

  Three consequences of the split are worth stating outright, because they change contracts
  callers may already depend on:

  - **The malformed-pair message now names the caller's own index.** Its `check item N` prefix is
    computed from an absolute offset, not from the position within whichever request carried the
    failure. Without that, a failure at relationship 1,003 reported as `check item 3` — the same
    misattribution the response-length guard exists to prevent, relocated into the diagnostic.
    This client's *per-item error* path is unaffected: it routes the item's own status straight
    through the error mapper and never carried a `check item N` prefix to get wrong. Only
    `spicedb-go` and `spicedb-java` index that path.
  - **`checked_at` is per response, and a response is now one chunk.** Results from a single
    request still share one token; an input large enough to be split carries more than one across
    the returned collection. Root DESIGN.md's bulk-check invariant has been re-scoped to match.
  - **A per-call timeout, and the retry budget, bound each request rather than the whole call.**
    Worst-case wall time for `n` checks is `ceil(n / 1000) x timeout`. This is deliberate: one
    deadline spanning every chunk would make a large check fail purely for being large, and a
    retry budget shared across chunks would let one flaky chunk exhaust the allowance for the
    rest. The docs now say so.

  Retry is applied per chunk, so a transient failure on the third chunk never re-sends the first
  two. `DEFAULT_CHECK_BATCH_SIZE` was declared but used only by a spec asserting it equalled
  1,000; it is now what does the chunking, and that spec now pins a value the code depends on.

- **2026-08-18**: **Security hardening — the guard that refuses to send credentials over
  plaintext to a non-loopback host now fails closed on targets it cannot vouch for.** The
  equivalent guard in this repo's C#, Rust, TypeScript and Java clients had a bypass:
  `"127.0.0.1:443@evil.com"` was read as loopback by a last-colon split while their transports
  parsed the same string as a URI, took `127.0.0.1:443` for *userinfo*, and connected to
  `evil.com` — sending the bearer token there in cleartext. **Ruby was not exploitable through
  that class**: grpc's C-core rejects that target outright ("Failed to parse port in name") and
  never contacts `evil.com`. But the guard was doing its own string split, and depending on
  C-core happening not to be fooled by one input is not a property worth relying on.

  Unlike most of the other clients, `.loopback_endpoint?` cannot be made to call the
  transport's own parser — the target goes to grpc's C-core, which parses it in C++ and exposes
  no Ruby-callable equivalent. So it now (1) refuses outright any endpoint containing `@`, `/`,
  `?`, `#`, or whitespace, the characters that can move the authority under URI parsing, and
  (2) splits what remains the way C-core's `SplitHostPort` does — a `[...]`-prefixed endpoint
  must be exactly `[host]` or `[host]:<digits>`, and a single-colon `host:port` is split only
  when the port is numeric. That numeric-port check is the one whose removal opened the C#
  bypass. `"127.0.0.1:443@evil.com"` and `"127.0.0.1:notaport"` now require
  `allow_insecure_remote_credentials: true` instead of being accepted as loopback. Every
  ordinary local target keeps working with no opt-in: `localhost:50051`, `127.0.0.1:50051`,
  `[::1]:50051`, `::1`, and `unix:` targets — the last now matched
  case-insensitively, since a URI scheme is case-insensitive and C-core normalizes `UNIX:`
  and dials the socket just the same, so the previous case-sensitive check refused a target
  the transport treats as local. That comparison is a regexp match rather than
  `String#casecmp`, which returns `nil` — and so raised `NoMethodError` from `nil.zero?` —
  for a non-ASCII-compatible encoding such as UTF-16LE.

- **2026-08-18**: `lookup_subjects` wrapped the ENTIRE streaming call in `with_retry`, so a
  mid-stream failure after some results had already been yielded to the caller retried the whole
  call from scratch and re-yielded results the caller had already seen. `LookupSubjects`' proto
  marks its cursor unimplemented, so unlike `lookup_resources`/`export_relationships` there is no
  resume mechanism at all -- retry there was structurally wrong, not merely unsafe in the usual
  "retry after first yield" sense. It now retries stream ESTABLISHMENT only, guarded by "zero
  results produced" -- the same guard `export_relationships` uses, and the same behavior as the
  other six clients. A transient `UNAVAILABLE` while opening the stream has delivered nothing and
  so has nothing to replay; failing the caller outright there (an earlier correction removed retry
  entirely) traded one defect for another, and left this the only client where opening a
  `LookupSubjects` stream during a rolling restart failed instead of retrying. Errors that survive
  the retry budget are still mapped to a native `SpiceDB::*` type rather than leaking a raw
  `GRPC::BadStatus`.

  `updates` remains deliberately un-retried in both windows: a watch stream is meant to run
  indefinitely, and a caller that wants one re-established should decide that itself using the
  resume token (`WatchEvent#changes_through`) the client already hands it.

  The shape shared by `lookup_subjects` and `export_relationships` moved into
  `SpiceDB::Retrying#with_establishment_retry`, which owns the zero-produced guard rather than
  leaving each call site to reimplement it (`should_retry_establishment?` only ever made the
  transient/budget decision). `SpiceDB::Client`'s own body shrank in the process, keeping it under
  the `Metrics/ClassLength` ceiling without raising it.
- **2026-08-18**: Verified `import_relationships`'s move onto the non-retrying `call_once` path
  (Group C Task 2) fully resolves the "retry re-iterates an exhausted one-shot enumerable" defect
  (Task 8(b)): a `File.foreach(...).lazy`/DB-cursor-style source that can only be iterated once is
  now consumed exactly once and fully delivered, and a failure on that single attempt raises
  rather than silently reporting `num_loaded == 0` as success. No further code change was needed;
  added regression coverage (`spec/client_import_relationships_one_shot_spec.rb`) proving both
  halves, verified discriminating against a reintroduced `with_retry` wrapping.
- **2026-08-18**: `export_relationships` drained an entire page's server stream into a Ruby Array
  before yielding a single relationship. `ExportBulkRelationships`' page-size field
  (`optional_limit`) bounds only the number of relationships in a SINGLE response message, unlike
  every other paginated RPC's page-size field, which bounds the whole call -- the server keeps
  streaming further response messages on the same call until the whole dataset has been sent. The
  per-page loop/cursor shape is unchanged (and was already correct); `call_export_relationships`
  now yields each relationship directly off the wire as its response message arrives instead of
  collecting a full page into an Array first, so the first relationship is available immediately
  regardless of export size -- an OOM risk this closes for the one API most likely to face the
  largest dataset in the system.
- **2026-08-18**: `SpiceDBProto::Client#initialize` leaked a `GRPC::Core::Channel` per secure
  (non-`insecure:`) construction. It built a channel with bare (uncomposed) credentials first,
  then -- for the secure path only -- built a SECOND channel with composed channel+call
  credentials and reassigned `@channel` to it, discarding the first. `#close` only ever closed
  whichever channel `@channel` currently referenced, so the first channel (and its connection)
  was never released. Credentials are now composed BEFORE the one-and-only channel is
  constructed, so exactly one `GRPC::Core::Channel` is created regardless of `insecure:`.
- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously no method accepted a timeout and no client-level default existed, so a
  SpiceDB instance that accepted a connection but never answered hung every caller forever — the
  connection looks fine at the transport level, so no error is produced and there is nothing for
  retry logic to act on.
  - Every unary method (`check_permission`/`check_permissions`/`check_any`/`check_all`, `write`,
    `delete_relationships`, `read_schema`, `write_schema`,
    `expand_permission_tree`, `experimental_register_relationship_counter`/
    `experimental_count_relationships`/`experimental_unregister_relationship_counter`,
    `reflect_schema`, `diff_schema`, `computable_permissions`, `dependent_relations`) now takes
    a `timeout:` keyword (seconds), passed through as grpc-ruby's `deadline:` call option (an
    absolute `Time`, computed fresh on each individual RPC attempt). Additive — omitting it is
    unaffected.
  - `Client.new`/`.new_plaintext`/`.new_system_tls` gained `default_timeout:` (seconds, default
    `SpiceDB::Client::DEFAULT_TIMEOUT_SECONDS = 30`), applied to any unary call that doesn't pass
    its own `timeout:`. 30s mirrors `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment
    cites `grpc/grpc-node#541`). There is deliberately no way to construct a client whose unary
    calls have no bound at all.
  - Streaming calls (`read_relationships`, `lookup_resources`, `lookup_subjects`, `updates`,
    `export_relationships`) do **not** accept `timeout:` and are **not** bound by
    `default_timeout` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default": these
    are long-lived by design (`updates` may legitimately run for the life of the process), and a
    30s cutoff would end a legitimate stream, which is a worse defect than the one this change
    fixes.
  - **Fix round 1 correction**: `import_relationships` (`import_bulk_relationships`) also takes
    a `timeout:` keyword, but — unlike the unary methods above — it is client-streaming, not
    unary, and is now explicitly **excluded** from `default_timeout`: its duration scales with
    the size of the caller's dataset, not with server latency, so no fixed default is correct
    for it (root DESIGN.md, "RULE: A unary call must have a deadline", clause 3, amended to
    cover client-streaming and bidirectional RPCs, not only server-streaming). `nil` (the
    default) now means unbounded there; passing `timeout:` still bounds the call. An earlier
    version of this fix incorrectly applied `default_timeout` to bulk imports, which would have
    silently aborted large, legitimate multi-minute loads at 30 seconds. `deadline_for` now
    returns `nil` for a `nil` input instead of raising.
  - `SpiceDB::DeadlineExceededError` (added earlier, but never actually produced by this client
    since nothing enforced a deadline) is now reachable: a timed-out call raises it, not a
    generic `SpiceDB::Error`. gRPC code `4` (`DEADLINE_EXCEEDED`) is not in `TRANSIENT_CODES`, so
    a timeout is never auto-retried.
  - `effective_timeout`/`deadline_for` (resolving a per-call `timeout:` against the client
    default, and converting seconds into the absolute `Time` grpc-ruby's stubs expect) moved into
    `SpiceDB::Retrying`, alongside the existing retry helpers.
  - New `spec/client_deadlines_spec.rb`, against a real `GRPC::RpcServer` whose handlers
    deliberately stall (a mocked `double`, used elsewhere in this suite, can't prove a deadline
    is actually enforced — grpc's deadline machinery lives below the mock): a unary call against
    a stub that never responds raises `DeadlineExceededError` well before the server's stall
    completes (not a hang, and covering both the `with_retry` check path and the `call_once`
    write/mutation path separately), a per-call `timeout:` overrides a much larger client
    default, a streaming call outlives a tiny unary default instead of inheriting it, and bulk
    import is both unbounded by the default and still honors an explicit `timeout:`. Every spec
    is wrapped in its own watchdog so a regression fails the suite instead of hanging CI.
  - New `examples/call_deadlines/`, run against a real SpiceDB rather than a mock: constructs a
    client via the documented `default_timeout:` keyword, overrides it per-call, and confirms
    bulk import is unbounded by default.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes:
  - `RESOURCE_EXHAUSTED` (gRPC code 8) is no longer retried. In SpiceDB it signals memory
    load-shed (retrying adds load to an already-overloaded server) or a deterministic
    `MaxDepthExceeded` (retrying can never succeed — it re-runs the most expensive class of
    check several times before surfacing the same error). Previously `SpiceDB::TRANSIENT_CODES`
    included `8` and `SpiceDB.transient?` treated `ResourceExhaustedError` as transient.
  - Mutations (`#write`, `#delete_relationships`, `#write_schema`, `#import_relationships`, and
    the `#experimental_register_relationship_counter`/`#experimental_unregister_relationship_counter`
    calls) are no longer retried on a transient error, even though the underlying gRPC code is
    retryable. A `WriteRelationships` carrying `OPERATION_CREATE` or preconditions is not
    idempotent: if it commits and the response is lost (a rolling restart, a proxy dropping the
    connection), a retry would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in
    fact succeeded, and the caller would wrongly conclude it had failed. Reads still retry
    automatically. All six mutation call sites previously went through `with_retry`; they now go
    through a new `call_once`, implemented as `with_retry(max_retries: 0)` so error-conversion
    stays in one place.
  - Backoff is now full-jitter (`rand * cap`) instead of plain exponential doubling. Without
    jitter, every client in a fleet retries on the same schedule after a server restart, turning
    the recovery into a thundering herd.

  `with_retry`/`call_once`/`backoff_delay` and the retry constants moved out of `Client` into a
  new `SpiceDB::Retrying` module (`lib/spicedb/retrying.rb`), included the same way
  `SpiceDB::CaveatContext` already is — the growth pushed `Client` over rubocop's
  `Metrics/ClassLength` limit, and this is the same pattern the class already uses. Public access
  (`SpiceDB::Client::MAX_RETRIES`, `SpiceDB::Client::BASE_RETRY_DELAY`) is unchanged, since Ruby
  resolves a class's constants through its included modules.

  `spec/errors_spec.rb` had two assertions that `RESOURCE_EXHAUSTED` was transient
  (`TRANSIENT_CODES` inclusion and `.transient?(ResourceExhaustedError.new)`); both are inverted,
  since the old assertions were exactly the defect this fixes. New coverage in
  `spec/client_retry_safety_spec.rb` (a mutation is attempted exactly once on a retryable error
  and on `RESOURCE_EXHAUSTED`; a read is retried; `RESOURCE_EXHAUSTED` is never retried on a
  read; backoff varies between calls).
- **2026-08-18**: `SpiceDB::CaveatContext#check_context_to_struct` and `#caveat_context_to_struct` had byte-identical bodies — two separately maintained copies of the same conversion, one per surface. That is exactly the place a future edit drifts the write path from the check path again, which is the divergence the converter convergence existed to close (the original defect was the write path stringifying while the check path did not). `#caveat_context_to_struct` is now an `alias` of `#check_context_to_struct`, re-exported with `module_function`, so the two are provably the same method rather than merely equal today. Both public names are kept — the call sites read correctly and confusing them would be a real bug — and behavior is unchanged. A spec asserts the alias relationship via `UnboundMethod#original_name`, so re-splitting them into two bodies fails CI.
- **2026-08-18**: `#updates` mapped an unrecognized watch operation to `:unknown`, a symbol used nowhere else in this client. The behavior was already safe — it was never `:touch`, unlike C#, TypeScript and Java, which mapped it to a write — but the name was unique to this one mapper, so a caller handling `:unspecified` everywhere else (the symbol this client already uses for an unrecognized `permissionship`) would miss it. The fallthrough arm now yields `:unspecified`, and `Update` documents the four possible `operation` symbols and why an unrecognized one must never be treated as a write. Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning must fail", clause 2. Breaking only for a caller matching on `:unknown`, which was undocumented; this client is unreleased.
- **`filter_to_proto` silently dropped `subject_id`/`subject_relation` when `subject_type` was
  not set, instead of raising.** `optional_subject_filter` was only built inside
  `if filter.subject_type`, so `SpiceDB::Filter.new(resource_type: 'document',
  subject_id: 'alice')` produced a proto `RelationshipFilter` with **no subject constraint at
  all**, while the `Filter` object itself still reported `subject_id == 'alice'` — a caller
  reading the object back would see the constraint they set; the server would not.
  `client.delete_relationships(f)` called with that filter deleted every relationship on every
  document, not just alice's — a correct-looking user-offboarding call that wipes the whole
  system. The wire's `SubjectFilter.subject_type` is a required field, so there is no way to
  express a subject ID/relation constraint without it, which makes silent widening the one unsafe
  resolution — `filter_to_proto` now raises `SpiceDB::InvalidArgumentError` naming the field that
  was set without `subject_type`, per root `DESIGN.md` "RULE: A conversion that cannot preserve
  meaning must fail", clause 1 (caller-supplied data the client cannot represent MUST raise a
  typed error). No pre-existing test asserted the silent-drop behavior, so none needed replacing.
- **`call_bulk_check` did not verify that `check_bulk_permissions` returned as many pairs
  as were requested.** The result `Array` was built by mapping `resp.pairs` directly, with
  nothing comparing its length to the number of items sent. The proto guarantees pairs are
  returned in request order but says nothing about count, so a response with fewer pairs
  than items would silently produce an `Array` shorter than `relationships` — every
  `results[i]` after the gap misaligned with `relationships[i]`, attributing one resource's
  answer to another. `call_bulk_check` now raises `SpiceDB::Error` naming both counts
  (`"check_bulk_permissions returned N pair(s) for M request item(s)"`) when they differ,
  before mapping any pair. It also now guards the malformed-oneof case — a
  `CheckBulkPermissionsPair` whose `response` oneof is unset (`pair.response.nil?`, i.e.
  neither `item` nor `error`) — the same way `spicedb-rust` already did, instead of
  raising an unhandled `NoMethodError` on a `nil` `item`.
- **Write-time caveat context was stringified on every value.** `relationship_to_proto`
  converted every `Relationship#caveat_context` value with
  `Google::Protobuf::Value.new(string_value: v.to_s)` regardless of its Ruby type — a
  number, a boolean, `nil`, a nested `Hash`, or a nested `Array` were all flattened to a
  string. A caveat like `now < 100` stored against the string `"50"` fails to evaluate,
  and fails *silently*, as a `CONDITIONAL_PERMISSION` result rather than an error. This
  is worse than the check-time equivalent (fixed separately, see the read-path entry
  below): a bad check-time context fails one call, but a bad **write-time** context is
  *persisted* — every future check against that relationship mis-evaluates, and
  re-checking with correct context never repairs it, only rewriting the relationship
  does. `relationship_to_proto` now dispatches on type via
  `SpiceDB::CaveatContext#caveat_context_to_struct`, reusing the same
  `check_context_value` per-value converter the check surface already used correctly —
  `with_caveat("x", { "n" => 42 })` now reads back the `Float` `42.0`, not the `String`
  `"42"` (`google.protobuf.Value.number_value` is a `double`, so an `Integer` round-trips
  as a `Float` on every path and every client — inherent to the proto, not a defect).
  Where a value genuinely cannot be represented (any type outside
  `nil`/`true`/`false`/`Numeric`/`String`/`Hash`/`Array`), conversion now raises
  `SpiceDB::InvalidArgumentError` naming the offending key, on both the check and write
  surfaces, rather than silently discarding or stringifying it.

  The caveat-context codec (`check_context_to_struct`, `caveat_context_to_struct`,
  `check_context_value`, `caveat_context_value_from_proto`, `struct_to_caveat_context`)
  moved out of `Client` into a new `SpiceDB::CaveatContext` module
  (`lib/spicedb/caveat_context.rb`), which `Client` includes. This was needed regardless
  of the fix above: `Client` was at 839 of rubocop's `Metrics/ClassLength` 850-line
  ceiling (itself raised from 830 by an earlier sub-project, which recorded that it must
  not be raised again), and adding the write converter inline would have pushed it over.
  Extracting the codec instead shrank `Client` to 800 lines, so the ceiling in
  `.rubocop.yml` is **lowered** to 810 — not raised.

- **`check_all` returned `true` for zero relationships.** Ruby's
  `Enumerable#all?` is vacuously `true` over an empty collection. Root
  `DESIGN.md`'s "An aggregate over zero checks is not a grant" clause names
  the hazard: a gate like `check_all(cs, "edit", *docs.map(&:to_rel))` was
  silently granted whenever the derived relationships array came up empty —
  a filter that matched nothing, an upstream returning `[]`. `check_all` now
  guards the empty case before the aggregate and returns `false` — it never
  reached the server for this case even before the fix, since
  `check_permissions` already short-circuits on an empty relationships
  array. `check_any` is unchanged — it was already correctly `false` on
  empty (`Enumerable#any?`).
- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 1,000 (matching SpiceDB's default `--max-delete-relationships-limit`, so the default `delete_relationships` call works against a stock server), not 10,000 — the earlier "10,000" correction in this file was itself wrong
- `updates` now maps errors raised mid-stream (e.g. a garbage-collected watch revision) to native `SpiceDB::*Error` types instead of leaking the raw `GRPC::BadStatus`. The watch stream is intentionally not retried on error — only mapped — since retrying a live server-stream mid-flight risks replaying updates.
- Per-item errors from `check_permission`/`check_permissions`/`check_any`/`check_all` (via `BulkCheckPermissions`) now raise the specific `SpiceDB::*Error` subclass (e.g. `SpiceDB::InvalidArgumentError`) instead of the generic base `SpiceDB::Error`.
- `SpiceDB.to_spicedb_error` no longer misreads `Google::Rpc::Status#details` (a repeated `Any` field, unrelated to the error message) as the exception message; it now falls back to `#message` whenever `#details` isn't a usable string, fixing the message text for the per-item `BulkCheckPermissions` fix above.
- **`with_expiration` was non-functional in both directions.** The client wrote and
  read `optional_expiration`; the field on `authzed.api.v1.Relationship` is
  `optional_expires_at`. Writing an expiring relationship raised a bare
  `ArgumentError: Unknown field name 'optional_expiration'` (not translated into a
  `SpiceDB::Error`, since `with_retry` only rescues errors responding to `#code`), and
  reading one silently returned `nil` — so an expiring relationship read back as
  permanent. No TTL-based grant could be built. The read path's `respond_to?` guard,
  which is what made the failure silent, has been removed so a future rename fails
  loudly.
- **Reading back any caveated relationship raised `NoMethodError`.**
  `relationship_from_proto` called `transform_values` on `Struct#fields`, which is a
  `Google::Protobuf::Map` and not a `Hash`. Since that one conversion backs
  `read_relationships`, `export_relationships` and `updates`, no deployment using
  caveats could read, export or watch relationships at all. Caveat context is now read
  by dispatching on `google.protobuf.Value`'s `kind` oneof, so numbers, booleans,
  nulls, nested maps and lists are returned with their Ruby types intact — the previous
  `&:string_value` would have returned `""` for all of them had it run.

  This fixed the **read** path only at the time. The **write** path's matching fix is
  below (**Write-time caveat context was stringified on every value**) — see that entry
  for the corrected round-trip behavior.

### Added

- **2026-08-19: four new examples, one per root `DESIGN.md` RULE that had no executed
  coverage in any client.** 15 example specs -> 19, 49 rspec examples -> 70, none renamed
  or removed. Group E Phase 3.

  - `examples/insecure_opt_in` — "RULE: Credentials over insecure transport require an
    explicit opt-in". Loopback plaintext needs no ceremony; a remote plaintext host is
    refused at construction, so the token never reaches a socket; and the named
    `allow_insecure_remote_credentials:` permits it. Five endpoints whose authority could
    move under URI parsing are each required to be refused — `127.0.0.1:443@evil.com`
    above all, where a last-colon split reads the host as `127.0.0.1` while the real
    authority is `evil.com`. Ruby hands its target to gRPC's C-core, which parses it in
    C++ out of this client's reach, so the rule requires failing closed on these rather
    than guessing.
  - `examples/unrepresentable_values` — "RULE: A conversion that cannot preserve meaning
    must fail", both directions. Unconvertible caveat context is refused with
    `SpiceDB::InvalidArgumentError` naming the offending key; a filter with `subject_id`
    and no `subject_type` is refused rather than silently widening, which for
    `delete_relationships` is the difference between deleting alice's relationships and
    deleting every relationship on every document. In the other direction, a
    permissionship this client has never seen must neither raise nor grant.
  - `examples/error_mapping` — "RULE: Error mapping must not lose the server's detail",
    written as the two recoveries the rule names: a stale ZedToken surfaces as
    `SpiceDB::OutOfRangeError`, recovered by dropping the token and re-reading at full
    consistency; a rotated token surfaces as `SpiceDB::UnauthenticatedError`, distinct
    from a transport fault. Nothing parses a message.
  - `examples/retry_policy` — "RULE: Automatic retry is for idempotent operations only",
    with attempts counted **server-side**, the only way to tell a retry from its absence:
    at the caller a transparently-retried success and a first-try success are identical. A
    read failing twice with `UNAVAILABLE` is retried to success in 3 attempts; a write
    failing the same way is attempted exactly once; `RESOURCE_EXHAUSTED` exactly once even
    on a read.

  Verified by mutation, 5 of 5 killing their example: disabling the loopback guard;
  dropping `OUT_OF_RANGE` from the code map; adding `RESOURCE_EXHAUSTED` to
  `TRANSIENT_CODES`; giving `call_once` a full retry budget; and letting an
  under-specified filter widen.

  The last three examples drive a `GRPC::RpcServer` stand-in, because neither R5 code is
  reachable from the real SpiceDB — verified, not assumed: a garbage ZedToken returns
  `INVALID_ARGUMENT` and the in-memory datastore never collects the revision, and a wrong
  preshared key returns **`PERMISSION_DENIED`, not `UNAUTHENTICATED`**. `error_mapping`
  asserts that real behaviour too, so a reader does not write a credential-refresh branch
  that can never run.

  On the error type raised by the insecure-transport guard: this client raises a plain
  `ArgumentError`, not `SpiceDB::InvalidArgumentError`. Across the seven clients that
  question currently has six different answers, so `insecure_opt_in` asserts what **this**
  client does rather than inventing a seventh — the divergence is recorded in-comment, not
  papered over.

- **2026-08-19**: The escape hatch `SpiceDB::Client#proto_client` gained the test and example
  it never had. The reader itself is unchanged and predates this entry — what is new is that
  its contract is now pinned: `spec/client_raw_escape_hatch_spec.rb` runs a real
  `GRPC::RpcServer`, drives the single-check `CheckPermission` RPC (which `#check_permission`
  deliberately routes around, so the gap is genuine) through the hatch's stubs, and asserts
  both the response and the `authorization` metadata the server received — mutation-verified
  by forcing the interceptor to a wrong token. It also pins that the hatch is an accessor,
  not a construction path (arity zero), and that the insecure-transport guard still refuses
  a non-loopback plaintext endpoint.

  `examples/raw_escape_hatch/` shows the two things the idiomatic API cannot express:
  `optional_transaction_metadata` on a write, and the single-check RPC. Both documented in
  DESIGN.md's "Escape Hatches", which previously said only that the proto client was
  "accessible ... for advanced use cases".

- **2026-08-19**: `SpiceDB::Client.new_custom_tls(endpoint, token, ca_cert:, client_cert:,
  client_key:)` — a third named constructor, for a SpiceDB fronted by a private or corporate CA and
  for mutual TLS. Purely additive; `new_plaintext` and `new_system_tls` are unchanged for callers.
  - `ca_cert:` — PEM root certificate(s) verifying SpiceDB's certificate. Replaces gRPC's built-in
    roots for that client rather than adding to them.
  - `client_cert:` / `client_key:` — the client's own PEM certificate chain and private key. Both
    must be supplied together; either alone raises `ArgumentError`, as does passing none of the
    three (that is `new_system_tls`, and a constructor named for custom trust material that
    silently used the compiled-in roots would be a quiet way to believe a private CA was
    configured when it was not).

  Without this, a private-CA deployment was unreachable: the credentials were built zero-argument
  in the proto tier and were never parameterizable, and gRPC's C-core compiles in its own
  `roots.pem`, so a CA installed in the host's trust store is not honoured. Root DESIGN.md, "RULE:
  A system-TLS constructor must reach a real server", permits `new_system_tls` to delegate to that
  bundled set precisely because a caller can supply their own material instead — which is now true.

  Trust material never changes whether TLS is used: there is no plaintext constructor that accepts
  it, and combining it with `insecure: true` on the private `Client.new` raises rather than
  discarding it (which would send the bearer token in cleartext behind a call site reading as
  though TLS were configured), so this cannot become a quieter route around root DESIGN.md, "RULE:
  Credentials over insecure transport require an explicit opt-in". That rule's existing loopback
  guard still applies first and unchanged.

  The three constructors now live in `SpiceDB::Connecting`, which `SpiceDB::Client` extends — an
  extraction with no call-site effect, made because `Client`'s `Metrics/ClassLength` ceiling is
  there to be respected rather than raised.

  ```ruby
  SpiceDB::Client.new_custom_tls('spicedb.internal:443', token,
                                 ca_cert: File.read('/etc/ssl/certs/internal-ca.pem')) do |client|
    ...
  end
  ```

- **2026-08-18**: Error mapping now carries the server's detail all the way to the caller, per root
  DESIGN.md, "RULE: Error mapping must not lose the server's detail". Purely additive.
  - Two new exception classes, both `SpiceDB::Error` subclasses:
    - `SpiceDB::OutOfRangeError` for gRPC code 11 (`OUT_OF_RANGE`), SpiceDB's code for an expired
      or garbage-collected ZedToken. It previously fell through to the base `SpiceDB::Error`, so
      the one recoverable error in a token-threading application was indistinguishable from an
      internal fault. Recovery is mechanical: discard the stale token and re-read at full
      consistency.
    - `SpiceDB::UnauthenticatedError` for gRPC code 16 (`UNAUTHENTICATED`) — a wrong, expired, or
      rotated API token, previously also indistinguishable from an internal fault. Distinct from
      `PermissionDeniedError`, which means the caller was identified but not allowed.
  - Every `SpiceDB::Error` now carries the `google.rpc.ErrorInfo` detail SpiceDB attaches to a
    status, on three new readers: `reason` (the name of an `authzed.api.v1.ErrorReason` enum value,
    e.g. `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`), `reason_domain` (`"authzed.com"` for SpiceDB),
    and `reason_metadata` (the specifics behind the reason, such as which precondition failed). The
    reason is surfaced exactly as the server sent it: a value a newer server knows and this client
    does not is passed through unchanged rather than coerced or rejected, per root DESIGN.md's
    "RULE: A conversion that cannot preserve meaning must fail", which requires server-supplied
    unknowns to degrade rather than raise. `reason` is `''` and `reason_metadata` `{}` when the
    server attached no `ErrorInfo`. `SpiceDB::Error.new(message)` still works unchanged — the three
    are optional keyword arguments. `reason_metadata` is a frozen copy: a caller cannot mutate it
    into the error, and mutating the Hash they passed in does not change what the error reports.
  - New file `lib/spicedb/error_details.rb` (`SpiceDB::ErrorDetails`), which reads the `ErrorInfo`
    off whichever shape the error arrived in: a `GRPC::BadStatus` (details in the
    `grpc-status-details-bin` trailer) or a `Google::Rpc::Status` (per-item bulk failure, details
    on the message itself). Extracted into its own module rather than added to `errors.rb`, so the
    error hierarchy stays small.

  ```ruby
  begin
    client.write(txn)
  rescue SpiceDB::FailedPreconditionError => e
    puts e.reason_metadata['precondition_resource_id'] if
      e.reason == 'ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE'
  rescue SpiceDB::OutOfRangeError
    # ZedToken expired or GC'd: drop it and re-read at full consistency.
  end
  ```

- **Caveat context on the check surface**: `check_permission`/`check_permissions`/`check_any`/`check_all` gain an optional `context:` keyword, and `SpiceDB::Relationship` gains a `check_context` field (with a matching `with_check_context(context)` builder). Previously a `CheckResult` with `permissionship == :conditional_permission` told you `missing_context` — the caveat parameter names SpiceDB couldn't evaluate — but there was no way to actually supply them, leaving the caller stuck. `context:` is a call-level default fanned out onto every relationship in the call (all checks go through `BulkCheckPermissions`, whose wire format attaches context per item — `CheckBulkPermissionsRequestItem#context`, proto field 4 — since `CheckBulkPermissionsRequest` itself has no context field); `relationship.with_check_context({...})` overrides that default for one relationship only, merged **key-by-key** with the call-level context (the item's keys win on conflict, but call-level keys the item doesn't mention are retained — NOT a wholesale replacement, which would silently drop shared keys and land the caller right back in `CONDITIONAL_PERMISSION`). An item with no `check_context` inherits `context:` unchanged; if neither is supplied, no `context` field is set on the wire at all (`nil`, not an empty `Struct`). Purely additive: `context:` defaults to `nil` on every check method and `check_context` defaults to `nil` on `Relationship`, so no existing call site changes.

  `check_context` is check-time-only and a **different concept** from the pre-existing `Relationship#caveat_context` (write-time context embedded in `optional_caveat`, persisted to SpiceDB). `check_context` has no wire representation on the write path at all — it's read exclusively by the check methods — so it can never leak into a write and silently alter a stored relationship's caveat. `with_caveat` and `with_check_context` never touch each other's field.

  ```ruby
  # Call-level: applies to every relationship checked in this call.
  results = client.check_permissions(consistency, "view", rel1, rel2, context: { now: 42 })

  # Per-item: overrides the call-level default for just this relationship.
  rel = SpiceDB::Relationship.from_triple("doc", "1", "viewer", "user", "alice")
                              .with_check_context({ now: 42 })
  result = client.check_permission(consistency, "view", rel)
  result.has_permission? # => true, now that the caveat could be evaluated
  ```

- `delete_relationships` gains optional `must_match:`/`must_not_match:` preconditions and a `limit:` override, mirroring spicedb-go's `client.DeleteRelationships` + `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`. Previously the only way to guard a delete with a precondition was to route it through `write` with a `Transaction`; now `delete_relationships` can build and send `optional_preconditions` directly. As with the Go client, preconditions are a per-request proto field, so a delete spanning multiple auto-paged pages re-evaluates them on every page rather than checking once for the whole operation — pair a precondition with a `limit:` large enough to cover all matches in one call for all-or-nothing semantics. Additive keyword arguments; existing `delete_relationships(filter)` callers are unaffected.

  ```ruby
  # Only delete viewers if the document still has an owner.
  owner_guard = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('doc1').with_relation('owner')
  viewer_filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('doc1').with_relation('viewer')
  client.delete_relationships(viewer_filter, must_match: [owner_guard])

  # Override the default 1,000-per-call page size.
  client.delete_relationships(viewer_filter, limit: 500)
  ```

- `SpiceDB::LookupResource` and `SpiceDB::LookupSubject` gain a `looked_up_at` field — the ZedToken (`String`) the lookup was evaluated against, closing the same read-your-writes gap `CheckResult#checked_at` closes for checks (see below). Both types are constructed exclusively by `lookup_resources`/`lookup_subjects`, so this has no effect on normal call sites; it only matters if code constructs `LookupResource.new(...)`/`LookupSubject.new(...)` directly (e.g. in tests), which now requires the extra keyword.

### Breaking

- **2026-08-18** (behavioral; new keyword argument): per root DESIGN.md, "RULE: Credentials over
  insecure transport require an explicit opt-in" -- `Client.new_plaintext` (and the underlying
  `SpiceDBProto::Client.new` with `insecure: true`) now refuse to construct a client for a
  non-loopback endpoint (loopback means `localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket
  target). Previously an insecure connection would send its bearer token in cleartext to any
  host: `BearerTokenInterceptor` merges the token into request metadata directly, because
  "channel credentials can't carry call credentials over a plaintext channel" -- necessary for
  `insecure: true` to work against loopback at all, but with nothing checking where the
  connection actually went. A new keyword argument, `allow_insecure_remote_credentials: true`,
  opts in explicitly when a caller genuinely means to send credentials in cleartext to a remote
  host; it must be passed alongside `insecure: true`/`new_plaintext`, since neither alone is
  sufficient for a non-loopback endpoint anymore. `new_plaintext`/`new_system_tls` against
  `localhost` are unaffected -- no code change needed for local development.

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `expand_permission_tree` on a large graph, or a filtered delete sweeping many pages) now fails
    with a deadline error where it previously ran to completion. Raise it with
    `Client.new_plaintext(..., default_timeout:)`, or pass `timeout:` on the individual call.
    There is deliberately no way to ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `write`, `delete_relationships`, `write_schema`, `import_relationships`, and the experimental
    counter register/unregister calls now surface a transient `UNAVAILABLE` to the caller on the
    first attempt rather than retrying. This is the correct default (replaying a non-idempotent
    write can report failure for a write that in fact committed), but a caller who was relying on
    the client to ride out a rolling restart must now retry themselves, knowing their own
    idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either, on reads
    or mutations.

- **2026-08-18**: Watch resumability. `updates` previously dropped
  `WatchResponse.changes_through` entirely and had no way to request
  `WATCH_KIND_INCLUDE_CHECKPOINTS`.
  - **Breaking**: `updates(object_types, start_revision: nil, include_checkpoints: false)` now
    returns `Enumerator<SpiceDB::WatchEvent>` instead of `Enumerator<SpiceDB::Update>`, and
    yields once per server response (a batch of updates) rather than flattening to one item per
    relationship update — a checkpoint response carries zero updates, so a per-update-only
    enumerator has no way to surface one at all.

    ```ruby
    WatchEvent = Data.define(:updates, :changes_through, :is_checkpoint)
    ```
  - `WatchEvent#changes_through` is the proto's `changes_through` -- "This token can be used
    in a subsequent WatchRequest to resume watching from this point." Without it, a consumer
    whose stream dropped could only restart from its original `start_revision` (reprocessing
    everything since, possibly past the GC window) or from head (silently losing every change
    in the gap).
  - New `include_checkpoints:` keyword (default `false`) requests
    `WATCH_KIND_INCLUDE_CHECKPOINTS` (plus `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since
    `optional_update_kinds` is empty-means-default and a non-empty list replaces rather than
    extends it) -- no prior way existed to ask for this at all. `WatchEvent#is_checkpoint` lets
    a caller tell "nothing changed, here is a fresh resume point" from "here are changes".
    Recommended if this SpiceDB instance is running behind a proxy that aborts idle
    connections.
  - `examples/watch_changes/` updated for the new `WatchEvent` shape and extended with a
    checkpoint-request example. New `spec/client_watch_resumability_spec.rb`: a watch event
    exposes a usable resume token, `include_checkpoints:` reaches the built `WatchRequest`,
    and a checkpoint event is distinguishable from one carrying updates.
    `client_watch_operation_mapping_spec.rb`'s cases updated for the new return type without
    weakening any existing assertion.
  - New `SpiceDB::WatchMapping` (`lib/spicedb/watch_mapping.rb`), included into `Client`
    alongside `CaveatContext`/`Retrying`: request-building and response-mapping for `#updates`
    moved out of `Client` the same way the caveat-context codec did, keeping `Client` under
    the `Metrics/ClassLength` ceiling `.rubocop.yml` deliberately does not raise.

### Changed

- **Breaking**: `check_permission`/`check_permissions` now return `SpiceDB::CheckResult`/`Array<SpiceDB::CheckResult>` instead of `Boolean`/`Array<Boolean>`. `CheckPermissionResponse#permissionship` is four-valued (`UNSPECIFIED`/`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`) — a caveated relationship whose context wasn't supplied at check time comes back `CONDITIONAL_PERMISSION`, which the old Boolean return silently collapsed into either a grant or a denial, losing the "SpiceDB couldn't evaluate this" signal entirely. `CheckResult` carries `permissionship` (Symbol: `:unspecified`/`:no_permission`/`:has_permission`/`:conditional_permission` — the same native symbols `lookup_resources`/`lookup_subjects` use), `missing_context` (`Array<String>` of caveat parameter names that weren't supplied — `[]` unless conditional), `checked_at` (the ZedToken `String` the check was evaluated against, enabling read-your-writes chaining that was previously unreachable), and `has_permission?` (`true` ONLY for `:has_permission`). `check_any`/`check_all` are unaffected in shape — still plain `Boolean` — but now explicitly count ONLY `:has_permission`; a `:conditional_permission` result does NOT count as a grant for either (deliberately fail-closed).

  **Ruby has no way to override truthiness** — every object except `nil`/`false` is truthy, unlike Python's `__bool__` hook. `if result` on a `CheckResult` is unconditionally `true`, including for a conditional permission. Anyone migrating from the old Boolean API by writing `if client.check_permission(...)` gets a silent grant on an unevaluated caveat. **Callers MUST use `result.has_permission?` — never test the result itself.**

  Before:
  ```ruby
  allowed = client.check_permission(consistency, "view", rel)
  grant_access if allowed # Boolean — no permissionship signal
  ```
  After:
  ```ruby
  result = client.check_permission(consistency, "view", rel)
  grant_access if result.has_permission? # NOT `if result` — every CheckResult is truthy in Ruby
  ```

- **Breaking**: `lookup_resources`/`lookup_subjects` now yield native result objects instead of bare `String` IDs, closing an over-grant risk: the previous string-only shape silently dropped `excluded_subjects` for wildcard (`user:*`) matches, so a caller iterating IDs alone could treat a wildcard-excluded subject as granted. `lookup_resources` now yields `SpiceDB::LookupResource` (`resource_id`, `permissionship`, `partial_caveat`); `lookup_subjects` now yields `SpiceDB::LookupSubject` (`subject: ResolvedSubject`, `excluded_subjects: Array<ResolvedSubject>`). Both use the new `permissionship` Symbol (`:unspecified` / `:has_permission` / `:conditional_permission`) and `SpiceDB::PartialCaveatInfo`. Mirrors spicedb-go's `client/lookup_types.go`/`lookup.go`, including its fallback to the deprecated `subject_object_id`/`excluded_subject_ids` proto fields for servers that don't yet populate the modern `subject`/`excluded_subjects` fields.

  Before:
  ```ruby
  client.lookup_resources(consistency, "document", "view", "user", "alice").each do |resource_id|
    grant(resource_id) # String only — no permissionship signal
  end
  client.lookup_subjects(consistency, "document", "doc1", "view", "user").each do |subject_id|
    grant(subject_id) # wildcard "*" treated as unconditional — over-grant risk
  end
  ```
  After:
  ```ruby
  client.lookup_resources(consistency, "document", "view", "user", "alice").each do |result|
    next unless result.permissionship == :has_permission # skip conditional

    grant(result.resource_id)
  end
  client.lookup_subjects(consistency, "document", "doc1", "view", "user").each do |result|
    excluded = result.excluded_subjects.map(&:subject_id).to_set
    next if result.subject.subject_id == '*' && excluded.include?(caller_id)

    grant(result.subject.subject_id)
  end
  ```

- **Breaking**: `expand_permission_tree` no longer leaks the raw protobuf `PermissionRelationshipTree` through `ExpandResult`. `ExpandResult#tree_root` is replaced by `ExpandResult#tree`, a native `SpiceDB::PermissionTree` built from new `Data.define` value types (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `PermissionTree`), mirroring the Go client's native expand tree.

  Before:
  ```ruby
  result = client.expand_permission_tree(consistency, "document", "1", "view")
  root = result.tree_root # proto PermissionRelationshipTree
  ```
  After:
  ```ruby
  result = client.expand_permission_tree(consistency, "document", "1", "view")
  tree = result.tree # SpiceDB::PermissionTree (native)
  ```

### Documentation

- **2026-08-18**: Pinned and documented stream release. Root DESIGN.md, "RULE: Abandoning a stream
  must release it", requires that stopping early actually tells the server to stop, and this client
  had no evidence either way: it contains no explicit cancel anywhere (a grep for "cancel" under
  `lib/` finds only `SpiceDB::CancelledError`), which reads from the outside exactly like the defect
  the rule describes. Measured against a real `GRPC::RpcServer`, it is not: `break`ing out of an
  Enumerator unwinds into the generator block's Fiber, which reaches grpc-ruby's own `ensure
  set_input_stream_done` in `ActiveCall#each_remote_read_then_finish`, which closes the core call
  synchronously. `first`/`take` and a collected mid-iteration Enumerator go the same way. New
  `spec/client_stream_release_spec.rb` locks this in for all five streaming methods, asserting on a
  *server-side* signal (a handler that streams until its own send fails) rather than on the
  consuming loop having exited, which proves nothing. Verified to have teeth: switching one method
  to consume its stream by external iteration -- the shape clause 3 of the rule warns about -- makes
  the specs fail.

  No code change, deliberately. Adding an explicit cancel means `return_op: true`, the only
  cancellable handle grpc-ruby offers, and that path freezes the call's metadata before the
  interceptor chain runs -- so on an insecure connection, where this client carries its bearer token
  in a `GRPC::ClientInterceptor`, every stream opened that way would go out unauthenticated, with
  every double-based spec still green. See `DESIGN.md`, "Stream lifecycle", before reaching for one.


- The three `experimental_*` relationship counter methods are now marked `@note Experimental` in their YARD docs to make clear they may change without following the backwards-compatibility mandate.

## 0.1.0 (2026-03-18)

Initial release of the idiomatic Ruby SpiceDB client.

### Features

- **Security-obvious constructors**: `SpiceDB::Client.new_plaintext` / `SpiceDB::Client.new_system_tls` make TLS posture explicit; block form for auto-close
- **Full API coverage**: all non-deprecated SpiceDB APIs exposed through idiomatic Ruby types
  - Permission checks (`check_permission`, `check_permissions`, `check_any`, `check_all`) via BulkCheckPermissions
  - Relationship CRUD with `Transaction` builder and preconditions
  - Streaming reads via `Enumerator` with transparent cursor pagination (supports `lazy`)
  - `lookup_resources` and `lookup_subjects`
  - Schema read, write, reflect, diff, computable permissions, dependent relations
  - Bulk import/export
  - Watch for relationship changes
  - Experimental relationship counters
- **`Data.define` value types**: `Relationship` and `Filter` are frozen, immutable value objects with `from_triple`, `from_tuple`, `with_caveat`, `with_expiration`
- **Explicit consistency**: every read requires a strategy (`full`, `min_latency`, `at_least`, `snapshot`, `at_least_or_full`, `at_least_or_min_latency`)
- **Exception hierarchy**: `SpiceDB::Error` with `PermissionDeniedError`, `NotFoundError`, `AlreadyExistsError`, `InvalidArgumentError`
- **Automatic retry**: exponential backoff for transient gRPC errors
- **Synchronous API**: uses Ruby's gRPC gem directly, no async complexity
- **9 examples** covering all major API surfaces, doubling as RSpec integration tests
- **Requires Ruby 3.2+** (for `Data.define`)
