# Rust client design

- **Expose native Rust values through the `spicedb` crate.**
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - `client`, `consistency`, `types`, and `error` separate transport, consistency, domain values, and failures; common values are re-exported at the crate root.
  - Requests and options own caller data, use private fields and builders, and never require protobuf or tonic values. Evolving results and enums are non-exhaustive. Domain values derive the usual comparison/debug traits where their data supports them.
  - `raw_proto()` is an authenticated secondary escape hatch with caller-managed deadlines and retries. `RPC_COVERAGE.md` maps every supported nondeprecated RPC to its native wrapper, including experimental Materialize operations.

- **Carry revisions as opaque `ZedToken` values.**
  - Every public revision input and result, consistency strategy, watch resume point, delete result, schema result, permission-set cursor, and Materialize event uses `ZedToken` (or `Option<ZedToken>` for absence).
  - `From<String>`, `From<&str>`, `AsRef<str>`, `Display`, and `into_string()` preserve the exact string contents. Authentication credentials and opaque pagination cursors remain distinct string values.
  - Read requests require explicit `Strategy`; `full()`, `min_latency()`, `at_least(ZedToken)`, and `snapshot(ZedToken)` select consistency. Named optional-token fallback helpers treat absent/empty tokens as their documented fallback; directly supplied empty revision tokens fail validation before RPC.

- **Make execution choices discoverable through enums.**
  - `CheckOptions::with_tracing(TracingMode::{Disabled, Enabled})` and `LookupResourcesOptions::with_debug(DebugMode::{Disabled, Enabled})` default to disabled and map independently to their wire controls.
  - `WatchOptions::with_checkpoints(CheckpointMode::{Exclude, Include})` defaults to Exclude. `with_content(WatchContent::{Relationships, RelationshipsAndSchema})` defaults to Relationships; schema content includes relationship updates as well.
  - `WildcardOption::{Include, Exclude}` selects lookup wildcard behavior. Exact subject filtering uses `SubjectRelationFilter::{Any, Direct, Named(String)}` and rejects unrepresentable filters instead of widening scope.
  - Result facts such as permission grants, checkpoint identity, deletion completion, and cursor progress remain booleans. Native enums prevent unknown option strings or flags from silently changing behavior.

- **Use one request and one extensible options value per operation.**
  - Relationship writes return `WriteResult`; its `revision` field is the native `ZedToken` to use in subsequent consistency or watch requests.
  - `check(PermissionCheck)` performs the singular RPC; `check_bulk(BulkCheckRequest)` preserves input order in chunks of 1,000 and retains each chunk's revision. Bulk item options and mixed consistency strategies are rejected before RPC. Echoed bulk requests must correspond exactly to inputs.
  - Required resources, subjects, permission names, and consistency belong to the request. Check context belongs to checks, never persisted relationships. Call context supplies defaults and item context overrides by key; invalid/nonfinite JSON fails explicitly.
  - `CheckResult` is must-use, retains native recursive optional debug information and missing context, and grants only for `Permissionship::HasPermission`. Unknown and conditional states never grant.
  - Relationships contain persisted tuple fields, caveat/context, and UTC expiration. Transactions borrow relationships and store native updates and preconditions. Trees and permission-set events use exclusive native enum variants; unknown updates never become writes or grants.

- **Bound operations and release canceled work.**
  - Unary operations default to one 30-second total budget, configurable by the builder or operation options. The budget includes connection readiness, retries, backoff, chunks, and pages, and propagates remaining time to RPCs.
  - Streams and imports have no default timeout; an explicit positive timeout covers their full lifetime. Dropping futures or streams releases transport work and upload producers; no detached work survives cancellation.
  - Read-only Unavailable/Aborted failures allow three retries with jittered backoff. ResourceExhausted and mutations are never automatically replayed. Streams retry establishment only before exposing data; watch requires explicit resume.
  - Clients are cheap Clone, Send, and Sync; independent operations share a channel without a global lock. Raw handles own the channel independently.

- **Preserve exact deletion scope and acknowledged progress.**
  - `delete_relationships(DeleteRequest::new(filter, NonZeroU32))` performs one bounded RPC. `delete_all_relationships_non_atomic(DeleteAllRequest::new(filter))` follows completion pages with a separate positive page size (default 1,000) and one total budget.
  - Preconditions apply to every page. Missing server cursors resume with the same filter; zero-count partial pages fail as nonprogress. Unknown completion states fail and never authorize another delete.
  - `DeleteResult` retains typed revision, count, completion, and optional cursor. Errors after acknowledged pages carry cumulative confirmed progress and the original typed cause, including malformed later responses.
  - Multi-page deletion is not atomic. An unacknowledged mutation may have committed and is never replayed automatically. Future drop cannot return progress; resumable cancellation uses bounded calls and persisted results.

- **Preserve native RPC data and diagnostics.**
  - Read/export/lookup streams support native context, supported limits and cursors, page sizes, total timeouts, and pinned revisions. LookupSubjects has no fabricated cursor. Wildcard exclusions and conditional permissions remain visible; results are not deduplicated.
  - Import sends batches of 1,000 with cancellation and producer-error propagation. Watch preserves updates, typed revisions, and checkpoint/schema events. Reflection/diff preserve native schema structures; counters preserve explicit still-calculating state.
  - Materialize wrappers preserve revisions, exclusive set/member changes, all cursor fields, completed/rebuild/schema events, file URLs and UTC timestamps. Unsupported consistency controls are not invented.
  - Typed errors retain status categories, structured reason/domain/metadata, and original sources. Local validation and timeouts use native categories without fabricated server statuses.

- **Require an explicit transport security policy.**
  - `new_system_tls(endpoint, token)` and `builder(endpoint, token)` use native trust roots. Custom CA injection and client identity are not currently supported.
  - `new_plaintext(endpoint, token, RemoteCredentialsPolicy::LoopbackOnly)` permits transport-parsed loopback. `RemoteCredentialsPolicy::AllowInsecureRemote` explicitly permits transmitting credentials without TLS to remote hostnames or IP addresses.
  - The normal builder offers `.plaintext().remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)`. The policy is available without raw access and defaults to LoopbackOnly.
  - Endpoint and token validation reject unsupported Unix targets, remote plaintext without opt-in, and invalid credentials before channel creation; secrets never appear in diagnostics.

- **Make completion deletion explicitly non-atomic.**
  - `delete_all_relationships_non_atomic` commits each batch transactionally. The whole operation is non-atomic: completed batches remain committed if a later batch fails.
  - `DeleteResult.revision` carries the native revision; failure progress preserves the last acknowledged revision and cumulative count.
  - Each nonempty server cursor becomes the next batch cursor. Empty or absent cursors retain the filter-based fallback; results and failure progress retain the last acknowledged cursor.

- **Verify observable contracts.**
  - Regression tests cover exact token conversion, enum wire mappings and defaults, strict bulk correspondence, safe unknown values, total budgets, transport cancellation, upload producer failures, typed diagnostics, exact filters, and deletion progress.
  - Security tests exercise parsed endpoints and opt-in behavior; gated integration tests complete real TLS handshakes. Examples assert behavior and are reconciled with their runner.
  - Run cargo formatting, tests, all-target build, clippy, documentation checks, and coordinated real-server examples. The root design governs cross-language compatibility and CI conventions.

- **Keep conditional lookup results distinct from unconditional grants.**
  - LookupResources preserves all permissionships by default, including conditional results. The optional `LookupPermissionshipFilter` selects only exact unconditional grants with `LookupPermissionshipFilter::HasPermissionOnly`.
  - Filtering happens client-side before yielding. Limits count yielded matches; skipped rows still advance cursors and page counts.
  - Conditional and unknown permissionships must never enter unconditional resource ID lists.

- **Expose native relationship filters for watch streams.**
  - `WatchOptions::with_filters` sends each native relationship filter to the server with OR semantics, including resource IDs, relations, and subject filters. Empty filters watch all relationships.
  - Object types and relationship filters are mutually exclusive and rejected locally when combined. Filter values are captured before streaming and preserve existing cancellation and resume behavior.
