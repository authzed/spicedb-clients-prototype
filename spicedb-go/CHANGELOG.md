# Changelog

## Unreleased

### Breaking Changes

- **2026-08-18** (behavioral; no signature change): per root DESIGN.md, "RULE: Credentials over
  insecure transport require an explicit opt-in" -- `NewPlaintext`/`NewWithOpts(..., WithInsecure())`
  now refuse to construct a client for a non-loopback endpoint (loopback means `localhost`,
  `127.0.0.0/8`, `::1`, or a `unix:` socket target). Previously an insecure connection would send
  its bearer token in cleartext to any host, including one a caller had no intention of trusting
  with it -- e.g. an `insecure: true` copied from a localhost example into a staging config. A new
  option, `WithInsecureAllowRemoteHost()`, opts in explicitly when a caller genuinely means to send
  credentials in cleartext to a remote host; it must be passed alongside `WithInsecure()`, since
  `WithInsecure()` alone is no longer sufficient for a non-loopback endpoint. `NewPlaintext` and
  loopback `NewWithOpts` usage are unaffected -- no code change needed for local development.

- **2026-08-18** (behavioral; no signature change): the entry below changes what existing,
  unmodified call sites do. It is listed here because it does not announce itself -- nothing
  fails to compile, and the difference only shows up under load. (Unlike the other six clients,
  this one gained no default deadline: a caller's `context.Context` has always been the bound,
  so no existing call's timing changed.)
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `Write`, `DeleteRelationships`, `WriteSchema`, `ImportRelationships`, and the
    experimental counter register/unregister calls now surface a transient `UNAVAILABLE` to the
    caller on the first attempt rather than retrying. This is the correct default (replaying a
    non-idempotent write can report failure for a write that in fact committed), but a caller who
    was relying on the client to ride out a rolling restart must now retry themselves, knowing
    their own idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either,
    on reads or mutations.

- **2026-08-18**: Watch resumability. `Updates` previously dropped
  `WatchResponse.changes_through` entirely and had no way to request
  `WATCH_KIND_INCLUDE_CHECKPOINTS`.
  - **Breaking**: `Updates(ctx, objectTypes, startRevision, ...)` now returns
    `iter.Seq2[client.WatchEvent, error]` instead of
    `iter.Seq2[rel.Update, error]`, and accepts variadic `WatchOption`s. It
    also now yields once per server response (batch of updates) rather than
    flattening to one yield per relationship update — a checkpoint response
    carries zero updates, so a per-update-only iterator has no way to
    surface it at all.

    ```go
    type WatchEvent struct {
        Updates        []rel.Update
        ChangesThrough string // resume token; pass as startRevision to resume after a dropped stream
        IsCheckpoint   bool   // true for a checkpoint event, which carries no Updates
    }
    ```
  - `WatchEvent.ChangesThrough` is the proto's `changes_through` --
    "This token can be used in a subsequent WatchRequest to resume watching
    from this point." Without it, a consumer whose stream dropped could
    only restart from its original `startRevision` (reprocessing
    everything since, possibly past the GC window) or from head (silently
    losing every change in the gap).
  - New `client.WithIncludeCheckpoints()` `WatchOption` requests
    `WATCH_KIND_INCLUDE_CHECKPOINTS` (plus
    `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since `OptionalUpdateKinds`
    is empty-means-default and a non-empty list replaces rather than
    extends it) -- no prior way existed to ask for this at all.
    `WatchEvent.IsCheckpoint` lets a caller tell "nothing changed, here is a
    fresh resume point" from "here are changes". Recommended if this
    SpiceDB instance is running behind a proxy that aborts idle
    connections, since a checkpoint keeps the stream alive even when there
    are no changes.
  - `examples/watch_changes/` updated for the new `WatchEvent` shape and to
    request checkpoints. New `client/watch_test.go` (no prior test coverage
    existed for `Updates` at all): a watch event exposes a usable resume
    token, `WithIncludeCheckpoints` reaches the built `WatchRequest`, a
    checkpoint event is distinguishable from one carrying updates, and a
    mid-stream error yields a zero-value `WatchEvent` with a mapped error.

- **2026-08-18**: `rel.Filter.ToProto()` now returns `(*v1.RelationshipFilter, error)` instead of
  a bare `*v1.RelationshipFilter`. Previously, `ToProto` nested `OptionalSubjectId`/
  `OptionalRelation` inside the `SubjectType != ""` check, so
  `rel.NewFilter("document").WithSubjectID("alice")` produced a proto `RelationshipFilter` with
  **no subject constraint at all**, while the `Filter` value itself still reported
  `SubjectID == "alice"` — a caller reading the struct back would see the constraint they set;
  the server would not. `DeleteRelationships(ctx, filter)` called with that filter deleted every
  relationship on every document, not just alice's — a correct-looking user-offboarding call
  that wipes the whole system. The wire's `SubjectFilter.subject_type` is a required field, so
  there is no way to express a subject ID/relation constraint without it, which makes silent
  widening the one unsafe resolution of the three available (throw / require the type / widen).
  `ToProto` now returns a new sentinel error, `rel.ErrInvalidFilter`, naming the field that was
  set without `SubjectType`, per root `DESIGN.md` "RULE: A conversion that cannot preserve
  meaning must fail", clause 1 (caller-supplied data the client cannot represent MUST raise a
  typed error). This ripples through every caller that previously called `Filter.ToProto()`
  directly: `rel.Txn.MustMatch`/`MustNotMatch` now return `error` (were `void`); the deferred
  conversion error from `client.WithDeleteMustMatch`/`WithDeleteMustNotMatch` (which build a
  `DeleteOption` and can't return an error directly) now surfaces from
  `client.DeleteRelationships` itself, as a `*client.Error` with `Code: CodeInvalidArgument`; the
  same wrapping applies to `client.ReadRelationships`, `client.ExportRelationships`, and
  `client.RegisterRelationshipCounter`. A filter with `SubjectType` set (alone, or with
  `SubjectID`/`SubjectRelation`) is unaffected — this only rejects the previously-silent
  no-`SubjectType` case.

  Before:
  ```go
  filter := rel.NewFilter("document").WithSubjectID("alice") // looks narrowed to alice
  revision, err := client.DeleteRelationships(ctx, filter)   // deletes EVERY document relationship

  var txn rel.Txn
  txn.MustNotMatch(filter) // void; no way to detect the same silent widening here
  ```
  After:
  ```go
  filter := rel.NewFilter("document").WithSubjectID("alice") // missing WithSubjectType
  revision, err := client.DeleteRelationships(ctx, filter)
  // err: "spicedb: filter has SubjectID set without SubjectType; the wire format requires
  // SubjectType whenever a subject constraint is present -- call WithSubjectType(...) before
  // WithSubjectID(...)"

  var txn rel.Txn
  if err := txn.MustNotMatch(filter); err != nil {
      log.Fatalf("failed to add precondition to transaction: %v", err)
  }

  // Fixed by supplying SubjectType, same as always intended:
  filter = rel.NewFilter("document").WithSubjectType("user").WithSubjectID("alice")
  ```

- **2026-08-18**: `rel.Relationship.ToProto()` now returns `(*v1.Relationship, error)` instead of a bare `*v1.Relationship`, and `rel.Txn.Create`/`Touch`/`Delete` now return `error` instead of nothing. Previously, if a relationship's caveat context couldn't be converted to a protobuf `Struct` (`structpb.NewStruct` fails on values it cannot represent), `ToProto` silently discarded the error and returned the relationship anyway — with the caveat name attached and an empty context. That corruption is written to SpiceDB and persists: every future check against that relationship mis-evaluates the caveat, and re-checking with correct context never repairs it, only rewriting the relationship does. `ImportRelationships` had the same defect for bulk import, corrupting an entire dataset the same way at scale. The check-path equivalent (`checkItemFromRel` in `client/checks.go`) already returned `CodeInvalidArgument` on conversion failure; this change gives the write path the same treatment instead of a second, worse behavior for the identical failure. No API shape change beyond the added error returns — the conversion itself, and everything it produces on success, is unchanged.

  Before:
  ```go
  var txn rel.Txn
  txn.Touch(r) // caveat context silently dropped on conversion failure

  proto := r.ToProto()
  ```
  After:
  ```go
  var txn rel.Txn
  if err := txn.Touch(r); err != nil {
      log.Fatalf("failed to add relationship to transaction: %v", err)
  }

  proto, err := r.ToProto()
  if err != nil { /* handle */ }
  ```

- **2026-08-17**: `Check`, `CheckOne`, and `CheckIter` now return a `CheckResult` (or `iter.Seq2[CheckResult, error]`) instead of a bare `bool`/`iter.Seq2[bool, error]`, so a caveated relationship whose context wasn't supplied at check time is distinguishable from a real denial instead of being silently collapsed to `false`. `CheckPermissionResponse.checked_at` — populated by the server on every check but never previously exposed by this client — is now reachable via `CheckResult.CheckedAt`, so read-your-writes is possible through the public API instead of requiring a raw gRPC stub. New type in `client/check_types.go`: `CheckResult{Permissionship, MissingContext, CheckedAt}` with `HasPermission() bool`, true only for `PermissionshipHasPermission`. `Permissionship` gains a fourth value, `PermissionshipNoPermission`, appended after `PermissionshipConditionalPermission` (not inserted alongside `PermissionshipUnspecified`) so the two pre-existing constants keep their `iota` values. `CheckAny`/`CheckAll` are unchanged in shape (still `(bool, error)`) but now count only `HasPermission()` results as granted — a Conditional result does not count, matching the fail-closed behavior of the new `CheckResult.HasPermission()`.

  `LookupResource`/`LookupSubject` also gain a `LookedUpAt string` field (from each response's `looked_up_at`), for the same read-your-writes reason — identical for every item in a single lookup stream.

  Before:
  ```go
  allowed, err := c.CheckOne(ctx, cs, "view", r)
  if err != nil { log.Fatal(err) }
  if allowed { /* ... */ } // true for both HAS_PERMISSION and (bug) CONDITIONAL_PERMISSION on some clients

  results, err := c.Check(ctx, cs, "view", rs...)
  for _, ok := range results { /* ok is a bare bool */ }
  ```
  After:
  ```go
  result, err := c.CheckOne(ctx, cs, "view", r)
  if err != nil { log.Fatal(err) }
  if result.HasPermission() { /* only true for a full grant */ }
  if result.Permissionship == client.PermissionshipConditionalPermission {
      // NOT a grant — result.MissingContext lists what the server needed
      // (e.g. ["now"]) and didn't get.
  }
  // Thread result.CheckedAt into consistency.AtLeast(...) for a later read
  // to observe this check.

  results, err := c.Check(ctx, cs, "view", rs...)
  for _, r := range results { r.HasPermission() /* ... */ }
  ```

  Write-surface audit for this change: `Write`, `DeleteRelationships`, and `WriteSchema` already returned a revision — no gap there. `ImportRelationships` (bulk import) does not, but that's because `ImportBulkRelationshipsResponse` has no `ZedToken` field in the proto at all — nothing for the client to expose.

- **2026-08-16** (carried forward from the cross-client error-hierarchy work, which made no CHANGELOG entries of its own): four sentinel errors were added to `client/errors.go` for `errors.Is` matching: `client.ErrUnavailable`, `client.ErrCanceled`, `client.ErrDeadlineExceeded`, `client.ErrResourceExhausted`. These complete the set alongside the six already present (`ErrNotFound`, `ErrAlreadyExists`, `ErrInvalidArgument`, `ErrFailedPrecondition`, `ErrPermissionDenied`, `ErrUnauthenticated`) — `client.Error.Code`/`client.ErrorCode` already had full gRPC-code coverage, but `errors.Is` sentinel matching was previously missing for these four codes. Additive; no existing sentinel changed meaning.

- **2026-08-14**: `LookupResources` and `LookupSubjects` now yield native result structs instead of bare ID strings, so callers no longer have to blindly trust an ID string — they can see whether a match is a full grant or conditional on caveat context, and (critically) which subjects are excluded from a wildcard `"*"` match. Dropping `excluded_subjects` was a real over-grant risk: a caller that saw `"*"` and nothing else had no way to know some subjects were carved out of that grant. New types in `client/lookup_types.go`: `Permissionship`, `PartialCaveatInfo`, `LookupResource`, `ResolvedSubject`, `LookupSubject`.

  Before:
  ```go
  for resourceID, err := range c.LookupResources(ctx, cs, "document", "view", "user", "alice") {
      if err != nil { log.Fatal(err) }
      fmt.Println(resourceID)
  }

  for subjectID, err := range c.LookupSubjects(ctx, cs, "document", "doc1", "view", "user") {
      if err != nil { log.Fatal(err) }
      fmt.Println(subjectID) // "*" here silently meant "everyone", excluded subjects were dropped
  }
  ```
  After:
  ```go
  for resource, err := range c.LookupResources(ctx, cs, "document", "view", "user", "alice") {
      if err != nil { log.Fatal(err) }
      if resource.Permissionship != client.PermissionshipHasPermission {
          continue // conditional match; resource.PartialCaveat lists what's missing
      }
      fmt.Println(resource.ResourceID)
  }

  for subject, err := range c.LookupSubjects(ctx, cs, "document", "doc1", "view", "user") {
      if err != nil { log.Fatal(err) }
      if subject.Subject.SubjectID == "*" {
          excluded := map[string]bool{}
          for _, e := range subject.ExcludedSubjects {
              excluded[e.SubjectID] = true // MUST check before granting to "everyone"
          }
      }
      fmt.Println(subject.Subject.SubjectID)
  }
  ```

- **2026-08-14**: `ExpandResult.TreeRoot` (a leaked `*v1.PermissionRelationshipTree` proto type) is replaced with `ExpandResult.Tree`, a native `PermissionTree` (see `client/expand_tree.go`: `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`, `TreeOperation`). No protobuf types are exposed from `ExpandPermissionTree` anymore.

  Before:
  ```go
  result, _ := c.ExpandPermissionTree(ctx, cs, "document", "1", "view")
  root := result.TreeRoot // *v1.PermissionRelationshipTree
  ```
  After:
  ```go
  result, _ := c.ExpandPermissionTree(ctx, cs, "document", "1", "view")
  tree := result.Tree // client.PermissionTree (native)
  ```

### Bug Fixes

- **2026-08-18**: **Security hardening — the guard that refuses to send credentials over
  plaintext to a non-loopback host now derives its host the way grpc-go does.** The equivalent
  guard in this repo's C#, Rust, TypeScript and Java clients had a bypass:
  `"127.0.0.1:443@evil.com"` was read as loopback by a last-colon split while their transports
  parsed the same string as a URI, took `127.0.0.1:443` for *userinfo*, and connected to
  `evil.com`. **Go was not exploitable through that class** — grpc-go's DNS resolver keeps host
  `127.0.0.1` and then fails on the unparseable port — but the guard was nonetheless doing its
  own string split, and relying on that particular input happening not to fool grpc-go is
  relying on an accident.

  `isLoopbackEndpoint` now resolves the target the way `grpc.NewClient` does (parse as a URI;
  if the scheme names no registered resolver, re-parse under the default scheme in authority
  form, as `ClientConn.initParsedTargetAndResolverBuilder` does) and judges **both** places a
  gRPC target can carry a host, each with the same `net.SplitHostPort` grpc-go's DNS resolver
  and `net.Dial` use:

  - the **endpoint** (the URI path), which is what gets resolved and dialed; and
  - the **authority** (the URI host), which for the `dns` scheme is the *nameserver* grpc-go
    queries — it hands `target.URL.Host` to `newNetResolver`, which dials it on port 53.

  Judging only the endpoint is not enough: `"dns://evil.com/localhost:50051"` has a loopback
  endpoint, but every lookup for it — including the `_grpc_config` TXT query whose service
  config grpc-go then *applies* — goes to `evil.com`, and whether the returned address is
  honoured comes down to host-resolver ordering. A target is now loopback only when the
  endpoint is loopback **and** the target carries no authority at all.

  **Not even a loopback authority is accepted.** `"dns://127.0.0.1:9999/localhost:50051"` is
  refused too. It is tempting to allow it — a nameserver on loopback looks like the same trust
  position as the system resolver — but it is not: redirecting the system resolver means
  editing `/etc/hosts` or `resolv.conf`, which needs root, while **binding a high UDP port on
  loopback needs no privilege at all**. On a shared host or a multi-process container any
  unprivileged process can answer the `_grpc_config` TXT query with a service config grpc-go
  applies, and answer the A/AAAA lookup with an address of its choosing. The endpoint string
  naming that nameserver is attacker-supplied, which is exactly what this guard defends
  against.

  Targets carrying URI userinfo, a query, a fragment, or a leftover `@`, `/`, `?`, `#`, or
  whitespace in the endpoint are refused outright. So `"127.0.0.1:443@evil.com"` (and its
  `passthrough:///` and `dns:///` forms), every `scheme://authority/endpoint` form including
  `"dns://evil.com/localhost:50051"` and `"dns://127.0.0.1:9999/localhost:50051"`, and
  `"unix://evil.com/var/run/spicedb.sock"` all now require `WithInsecureAllowRemoteHost`
  instead of being accepted as loopback. Every ordinary local target keeps working with no
  opt-in: `localhost:50051`, `127.0.0.1:50051`, `[::1]:50051`, `[::1]`, `::1`, `unix:` and
  `unix-abstract:` targets (matched case-insensitively on the scheme, as grpc-go itself does),
  and the authority-less `passthrough:///` and `dns:///` forms of each.

  Host extraction now follows the same three-step sequence as grpc-go's DNS resolver (bare IP
  literal, then `net.SplitHostPort`, then the same split with a port appended so a bare
  `[::1]` is de-bracketed by the parser). It replaces a `strings.Trim(host, "[]")` that
  stripped any number of brackets from either end, so `"]127.0.0.1["`, `"[::1"` and `"::1]"`
  had all reported loopback — harmless, since `net.SplitHostPort` rejects them and nothing
  could be dialed, but hand-rolled string surgery next to a parser is the pattern that
  produced the original bypass.

- **2026-08-18**: Abandoning a streaming iterator leaked the gRPC stream and the server-side
  dispatch. Every streaming call that returns an iterator (`Updates`, `LookupResources`,
  `LookupSubjects`, `ReadRelationships`, `ExportRelationships`) returns an `iter.Seq2` that stops on
  `if !yield(...) { return }` -- i.e. when the consumer `break`s -- while the underlying
  `grpc.ClientStream` was left open on the caller's own context. grpc-go's
  `ClientConn.NewStream` contract is explicit that unless the context is cancelled, `Close` is
  called, or `RecvMsg` drains to a non-nil error, "a goroutine and a context will be leaked";
  SpiceDB, for its part, keeps a dispatch open per abandoned stream. So the common idiom
  `for e, err := range c.Updates(ctx, types, "") { ...; break }` with a long-lived `ctx` leaked
  one HTTP/2 stream and one server-side dispatch permanently. `Client.Close()` was no help: it
  releases the whole connection, not one stream. Each iterator now derives its own cancellable
  context and cancels it on the way out, so every exit path -- consumer `break`, mid-stream
  error, or normal exhaustion -- releases the stream. Per root DESIGN.md, "RULE: Abandoning a
  stream must release it", clause 2: the transport must actually release, and that is what the
  new tests in `client/stream_release_test.go` assert, by parking a stub server handler on its
  own `stream.Context().Done()` and failing if the server never observes the cancellation. No
  API change -- the caller's context is still honored exactly as before, and cancelling it still
  cancels the stream.

- **2026-08-18**: The fix above covered every streaming call that returns an iterator -- the
  receiving side. `ImportRelationships`, the one client-streaming call, sends rather than
  receives, and had the identical grpc-go leak on its own early-return paths: a relationship
  whose caveat context failed `ToProto()` (a documented, reachable outcome per the method's own
  doc comment) returned immediately, leaving the client-streaming call opened at
  `client/bulk.go:29` neither cancelled, closed, nor drained -- on the caller's own, typically
  long-lived, context. A `Send` failure mid-batch left the same call open the same way. Both
  paths now run inside a `context.WithCancel` derived at the top of the function and deferred
  once, the same pattern `ExportRelationships` already used. "Every streaming call" above was an
  overclaim while this gap existed -- five of six, not six of six; it is accurate now.
  `TestImportRelationships_ToProtoFailureReleasesTheStream` in
  `client/stream_release_test.go` covers it the same way as the other five: against a real
  in-process server, asserting the server's own `Recv` observed the cancellation, not that the
  call returned an error.

- **2026-08-18**: `Close()` panicked on a `Client` that never opened a connection. The connection
  handle is unexported, so a zero-value `client.Client{}`, or a
  `&spicedbgoproto.Client{PermissionsServiceClient: stub}` assembled by hand to point at a test
  double, carries a nil one that no constructor could produce -- and `Close` is precisely the
  method such a value reaches, via a `defer c.Close()` copied from production code. Both tiers
  now treat a connectionless client as a no-op close (returning nil) rather than dereferencing
  nil, so a test double no longer crashes on a line unrelated to what it tests. Nil receivers are
  covered too.
- **2026-08-18**: `Client` had no way to release its underlying gRPC connection deterministically
  -- every streaming call that returns an iterator (`ReadRelationships`, `LookupResources`,
  `LookupSubjects`, `Watch`, `ExportRelationships`) shares one connection for the life of the
  process, per root DESIGN.md, "RULE: Abandoning a stream must release it". Added
  `Client.Close() error`, idempotent (backed
  by an atomic CompareAndSwap in the proto layer, since `grpc.ClientConn.Close` is not documented
  safe to call twice). All twelve examples now `defer c.Close()` after construction.
- **2026-08-18**: `CheckIterWithContext`'s internal `flush` cleared the accumulated batch AFTER
  calling `yield` on the success path, but returned on the error path (`return yield(CheckResult{},
  err)`) BEFORE clearing it. Go's iterator contract treats `yield` returning `true` as "keep
  going", so a consumer that logged the error and continued (rather than `break`ing, which is why
  this had never been noticed) left the failed batch in place: the next relationship pushed it
  past `defaultCheckBatchSize`, so `flush` resent the SAME failing batch plus one on every
  subsequent element -- an unbounded, monotonically growing batch until it crossed SpiceDB's
  `maxBulkCheckCount` and the transient error became a permanent `InvalidArgument`. `flush` now
  clears the batch unconditionally before invoking `yield`, on both paths.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". The fix lives entirely in `proto-clients/spicedb-go-proto/client.go`'s
  `NewClient` — Go's retry is a gRPC service-config `retryPolicy` shared by every RPC on a service,
  so every idiomatic `spicedb-go/client.Client` method (which all dial through `NewClient`)
  inherits this fix with no change to `spicedb-go/client` itself. Two changes:
  - The JSON service config now has **two** `methodConfig` entries instead of one: a
    service-level entry (unchanged shape) carrying the `retryPolicy` for all four services, and a
    new method-level entry for the seven RPCs that are not safely retryable —
    `WriteRelationships`, `DeleteRelationships`, `ImportBulkRelationships`, `WriteSchema`,
    `ExperimentalRegisterRelationshipCounter`, `ExperimentalUnregisterRelationshipCounter`, and
    `ExperimentalService.BulkImportRelationships` — with no `retryPolicy` at all. gRPC's own
    service-config resolution (`google.golang.org/grpc/clientconn.go`'s `getMethodConfig`) always
    prefers an exact `/service/method` match over a `/service/` wildcard, so these seven RPCs get
    no retry policy, overriding the broader entry, while every other RPC on the same services
    still retries. A `WriteRelationships` containing `OPERATION_CREATE`, or any request carrying
    preconditions, is not idempotent: if it commits and the response is lost, a retry surfaces
    `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in fact succeeded.

    `BulkImportRelationships` was missing from the initial version of this list (caught in
    review): it's the deprecated RPC `ImportBulkRelationships` superseded, still present on the
    wire (`option deprecated = true`, not removed), and still directly reachable through
    `Client.ExperimentalServiceClient`, which this package exports — deprecation is a
    documentation signal, not an enforcement mechanism. It is exactly as non-idempotent a
    client-streaming bulk write as its replacement. Audited every other RPC on
    `PermissionsService`, `SchemaService`, and `ExperimentalService` (including the rest of
    `ExperimentalService`'s deprecated surface — `BulkExportRelationships`,
    `BulkCheckPermission`, `ExperimentalReflectSchema`, `ExperimentalComputablePermissions`,
    `ExperimentalDependentRelations`, `ExperimentalDiffSchema`) against the `.proto` sources:
    every one of them is a read, so none needed adding.
  - `RESOURCE_EXHAUSTED` is removed from `retryableStatusCodes` (now just `UNAVAILABLE`,
    `ABORTED`). In SpiceDB it signals memory load-shed or a deterministic `MaxDepthExceeded`,
    never a transient hiccup. `ABORTED` is unchanged and stays retryable — SpiceDB maps datastore
    serialization conflicts to it, and those transactions are rolled back.

  Backoff jitter is unaffected by this change: grpc-go's retry implementation already randomizes
  every computed backoff by a factor of 0.8-1.2 (per gRFC A6), independent of and not configurable
  through the JSON service config. That is narrower than full jitter (`uniform(0, cap)`) but is
  built into gRPC's retry mechanism itself, not something this client authors — Go is the one
  client of the seven where jitter was already present before this change.

  The service-config JSON literal is now a named `retryServiceConfig` const (was inlined in
  `NewClient`) so `client_test.go` can parse and assert on the exact config installed, rather than
  a separately maintained copy that could drift. New tests:
  `TestRetryServiceConfig_MutationsHaveNoRetryPolicy` and
  `TestRetryServiceConfig_ReadsRetryButNotResourceExhausted` (structural), plus
  `TestNewClient_MutationIsAttemptedExactlyOnceOnRetryableError`,
  `TestNewClient_ResourceExhaustedIsNeverRetried`, and
  `TestNewClient_DeprecatedBulkImportIsAttemptedExactlyOnceOnRetryableError` in `retry_test.go`
  (behavioral, real bufconn gRPC server). No pre-existing test asserted a mutation retried or
  `RESOURCE_EXHAUSTED` was transient, so none needed inverting —
  `TestNewClient_RetriesTransientErrors` already covered (and continues to cover) that a read
  (`CheckPermission`) retries on `UNAVAILABLE`.
- **2026-08-18**: the caveat-context conversion error from `rel.Relationship.ToProto` (and therefore from `rel.Txn.Create`/`Touch`/`Delete`) was neither a matchable sentinel nor key-naming, unlike its sibling `rel.ErrInvalidFilter`. A new sentinel, `rel.ErrInvalidCaveatContext`, is now wrapped by every such error, so a caller can use `errors.Is` instead of string-matching. The message also **names the offending key**: `structpb.NewStruct` converts the whole map at once and reports only the value's Go type (`invalid type: chan int`), never which entry held it, which on a context map with many keys left the caller guessing. A new exported helper, `rel.CaveatContextToStruct`, converts per key and identifies the entry — the same thing C#, Java and Ruby already report for this failure. That helper is now the single converter for **both** caveat-context surfaces: write-time (`Relationship.CaveatContext`, via `ToProto`) and check-time (the merged context in `client.checkItemFromRel`, which previously called `structpb.NewStruct` directly), so the two can never drift apart in what they accept or how they describe a failure. Purely additive: no signature changed, and the check path still returns a `CodeInvalidArgument` `*client.Error` — it now additionally satisfies `errors.Is(err, rel.ErrInvalidCaveatContext)`.

  The sentinel lives in `rel`, not wrapped as a `*client.Error` inside `Txn.Create`/`Touch`/`Delete`, because `rel` is deliberately client-independent (its package doc says so) and `client` imports `rel` — wrapping there would be an import cycle. This matches `ErrInvalidFilter` exactly: `rel` returns the sentinel, and `client` wraps it as a `*client.Error` with `CodeInvalidArgument` at its own API boundaries (`ImportRelationships`, `ReadRelationships`, `DeleteRelationships`, the check surface).
- **2026-08-18**: `Check`/`CheckWithContext` did not verify that `CheckBulkPermissions` returned as many pairs as were requested — `results` was sized off `len(resp.GetPairs())` instead of `len(items)`, and nothing compared the two. The proto guarantees pairs are returned in request order but says nothing about count, so a short response silently desynced `results[i]` from `rs[i]` for every item after the gap: one resource's answer attributed to another. Three concrete consequences, all now fixed: `CheckOne`/`CheckOneWithContext` did `return results[0], nil` and **panicked with index-out-of-range on a zero-pair response** (the same panic fixed in spicedb-rust); `CheckAll`/`CheckAllWithContext` over a short response returned `true` where the dropped checks would have denied; and `CheckAny`/`Check` returned a slice the caller could not index against its own request. A length mismatch in either direction now returns a `CodeInternal` error naming both counts. A `CheckBulkPermissionsPair` with neither `Item` nor `Error` set is likewise rejected instead of falling through to the item's zero value (which reads as `PERMISSIONSHIP_UNSPECIFIED`, indistinguishable from a genuine denial) — mirroring spicedb-rust's malformed-oneof guard. This is the same guard the other six clients received.
- **2026-08-18**: `rel.UpdateFromProto` left `Update.Operation` at an unnamed zero value for a watch operation this client does not recognize (`OPERATION_UNSPECIFIED`, or any future wire value): the `switch` had no `default` and `UpdateOperation`'s constant block started at `iota + 1`, so nothing named or documented what the caller actually received. The behavior was already safe — it was never a write, unlike C#, TypeScript and Java, which mapped it to `TOUCH` — but a caller had no name to compare against and no doc telling them the case existed. `UpdateOperationUnspecified` is now the named zero value of the constant block (existing constants keep their numeric values: Create 1, Touch 2, Delete 3), the `switch` has an explicit `default` arm returning it, and both are documented as "never treat this as a write." Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning must fail", clause 2. Purely additive.
- **2026-08-18**: `ImportRelationships` now stops and returns a `CodeInvalidArgument` error naming the failing conversion if any relationship's caveat context cannot be converted to protobuf, instead of silently importing it with the caveat name attached and no context (see the `ToProto`/`Txn` breaking change above for why this matters more for a write than a check).
- **2026-08-18**: `CheckAll`/`CheckAllWithContext` now return `false` for zero relationships instead of vacuously `true`. Root `DESIGN.md`'s "An aggregate over zero checks is not a grant" clause names the hazard: Go's bare `for` loop aggregate falls through to `return true, nil` once it runs out of results, so `CheckAll(cs, "edit", docs.map(toRel)...)` was silently granted whenever the derived relationship slice came up empty — a filter that matched nothing, an upstream returning `nil`, a defensive empty slice. The guard runs before the RPC, so an empty call never reaches the server (unchanged from before: `CheckWithContext` already short-circuited on zero relationships). `CheckAny`/`CheckAnyWithContext` are unchanged — already correctly `false` on empty. No API shape change.
- **2026-08-17**: A check-time caveat context that `structpb.NewStruct` cannot convert (e.g. an unsupported value type) now returns an error from `Check`, `CheckWithContext`, `CheckOne(WithContext)`, `CheckAny(WithContext)`, `CheckAll(WithContext)`, and `CheckIter(WithContext)`, instead of silently sending the request with no context field. Previously the conversion error was discarded and the caller got back a `CONDITIONAL_PERMISSION`/`NO_PERMISSION` result indistinguishable from "the server legitimately needed more context than you supplied" — now the two cases can't be confused. No API shape change.
- **2026-08-15**: `defaultDeletePageSize` (the default `DeleteRelationships` page size) is now 1,000, not 10,000. SpiceDB's default `--max-delete-relationships-limit` is 1,000, so a default (no-`DeleteOption`) `DeleteRelationships` call against a stock server previously failed with `provided limit 10000 is greater than maximum allowed of 1000`. No API shape change — only the default value sent when `WithDeleteLimit` isn't used.
- **2026-08-14**: `client.Error.Code` is now a native `client.ErrorCode` enum (`CodeUnknown`, `CodeNotFound`, `CodeAlreadyExists`, `CodeInvalidArgument`, `CodeFailedPrecondition`, `CodePermissionDenied`, `CodeUnauthenticated`, `CodeUnavailable`, `CodeResourceExhausted`, `CodeAborted`, `CodeDeadlineExceeded`, `CodeCanceled`, `CodeInternal`), replacing the raw `google.golang.org/grpc/codes.Code` that was previously exposed on the field. This closes a gap left by the earlier native-error-mapping fix, which mapped errors into `*client.Error` but left the raw gRPC code type on the struct. `errors.Is`/sentinel matching (`ErrNotFound`, etc.) is unchanged for callers; `errors.Unwrap` still exposes the underlying gRPC status error as an escape hatch. Any code that compared `err.(*client.Error).Code` against `codes.X` must switch to comparing against `client.CodeX`.

  Before:
  ```go
  if cerr, ok := err.(*client.Error); ok && cerr.Code == codes.NotFound {
      // handle not found
  }
  ```
  After:
  ```go
  if cerr, ok := err.(*client.Error); ok && cerr.Code == client.CodeNotFound {
      // handle not found
  }
  // or, unchanged: errors.Is(err, client.ErrNotFound)
  ```

### Features

- **2026-08-18**: Error mapping now carries the server's detail all the way to the caller, per root
  DESIGN.md, "RULE: Error mapping must not lose the server's detail". Purely additive.
  - New sentinel `ErrOutOfRange` and new `ErrorCode` value `CodeOutOfRange`. `OUT_OF_RANGE` is
    SpiceDB's code for an expired or garbage-collected ZedToken; it previously fell through to
    `CodeUnknown` and matched no sentinel, so the one recoverable error in a token-threading
    application was indistinguishable from any other unmapped failure. Recovery is mechanical:
    discard the stale token and re-read at full consistency. `CodeOutOfRange` is appended to the
    `ErrorCode` constant block, so every existing constant keeps its numeric value.
  - New fields on `*Error`: `Reason`, `ReasonDomain`, and `ReasonMetadata`. These carry the
    `google.rpc.ErrorInfo` detail SpiceDB attaches to a status — `Reason` is the name of an
    `authzed.api.v1.ErrorReason` enum value (e.g. `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`), and
    `ReasonMetadata` is the specifics behind it, such as which precondition failed. The reason is
    surfaced exactly as the server sent it: a value a newer server knows and this client does not
    is passed through unchanged rather than coerced or rejected, per root DESIGN.md's
    "RULE: A conversion that cannot preserve meaning must fail", which requires server-supplied
    unknowns to degrade rather than raise. `Reason` is empty and `ReasonMetadata` nil when the
    server attached no `ErrorInfo`.

  ```go
  var spiceErr *client.Error
  if errors.As(err, &spiceErr) && spiceErr.Reason == "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED" {
      log.Printf("depth limit was %s", spiceErr.ReasonMetadata["maximum_depth_allowed"])
  }
  if errors.Is(err, client.ErrOutOfRange) {
      // ZedToken expired or GC'd: drop it and re-read at full consistency.
  }
  ```

- **2026-08-17**: `Check`, `CheckOne`, `CheckAny`, `CheckAll`, and `CheckIter` each gain a `*WithContext` counterpart (`CheckWithContext`, `CheckOneWithContext`, `CheckAnyWithContext`, `CheckAllWithContext`, `CheckIterWithContext`) for supplying caveat context on a check. This closes a real gap: `CheckResult.MissingContext` (added above) told a caller a check needed caveat context like `"now"`, but there was previously no parameter anywhere on the check surface to supply it, making the information non-actionable. Purely additive — every existing call site (`client.Check(ctx, cs, "view", r1, r2)`, `client.CheckOne(...)`, etc.) is completely unaffected; the non-context methods are unchanged in signature and now simply delegate to their `*WithContext` counterpart with a `nil` context.

  Each `*WithContext` method takes an extra `checkContext map[string]any` parameter, positioned right after `permission` and before the (still variadic) relationships, e.g. `CheckWithContext(ctx, cs, permission, checkContext, rs ...rel.Relationship)`. New field on `rel.Relationship`: `CheckContext map[string]any`, set via the new `rel.Relationship.WithCheckContext(map[string]any)` builder, for supplying context to just one relationship in a call — distinct from the existing `CaveatContext`/`WithCaveat`, which is stored with a relationship on write, not sent on check. The two merge key by key for each item — item keys win on conflict, call-level keys absent from the item are retained, never wholesale-replaced, so a per-item override can't silently drop a shared key the caveat still needs.

  ```go
  // Existing call sites: unaffected.
  results, err := c.Check(ctx, cs, "view", r1, r2, r3)
  allAllowed, err := c.CheckAll(ctx, cs, "view", r1, r2, r3)

  // New: call-level default context, applied to every relationship in the call.
  result, err := c.CheckOneWithContext(ctx, cs, "conditional_view",
      map[string]any{"now": time.Now().Unix()}, r)

  // New: per-item context overrides a call-level default for just that one
  // relationship (merged key by key, not replaced).
  results, err = c.CheckWithContext(ctx, cs, "view",
      map[string]any{"now": N, "region": "us"},
      r1,                                                  // gets {"now": N, "region": "us"}
      r2.WithCheckContext(map[string]any{"region": "eu"}), // gets {"now": N, "region": "eu"}
  )
  ```

  An earlier version of this change added `opts ...CheckOption` to the existing methods, which forced `Check`/`CheckAny`/`CheckAll`'s `rs ...rel.Relationship` to become `rs []rel.Relationship` (Go allows only one variadic parameter, and it must be last, so the relationships parameter had to give up that slot to make room for trailing options). That degraded the common call site for every caller, including the majority who never touch caveat context, to serve the minority who do — reverted in favor of the parallel `*WithContext` methods above, which keep every existing signature byte-for-byte unchanged. See `spicedb-go/DESIGN.md` ("Checks" / "Check-time caveat context") for the full rationale and the merge rule.

- **2026-08-15**: `DeleteRelationships` now accepts variadic `DeleteOption`s, reaching the proto's `optional_preconditions` and `optional_limit` fields that were previously unset by the client. Additive — existing `c.DeleteRelationships(ctx, filter)` calls are unaffected (no preconditions, 1,000-item page size, partial deletions allowed, same as before). New: `client.WithDeleteMustMatch(filter)`/`client.WithDeleteMustNotMatch(filter)` add MUST_MATCH/MUST_NOT_MATCH preconditions (built the same way as `rel.Txn.MustMatch`/`MustNotMatch`) that guard the delete, rejecting it if unsatisfied; `client.WithDeleteLimit(n)` overrides the default 1,000-per-call page size. See `spicedb-go/DESIGN.md` ("Deletions") for the semantics of combining preconditions with auto-paging. New example: `examples/delete_relationships/`.

  ```go
  // Before (still works, unchanged):
  revision, err := client.DeleteRelationships(ctx, filter)

  // After (new, optional):
  revision, err := client.DeleteRelationships(ctx, filter,
      client.WithDeleteMustMatch(ownerGuard),
      client.WithDeleteLimit(1000),
  )
  ```
- **2026-08-14**: Added automatic retry with exponential backoff for transient gRPC errors, configured via gRPC's built-in service-config `retryPolicy` in `NewClient`'s dial options (proto-client tier). This entry set `retryableStatusCodes` to `UNAVAILABLE`, `RESOURCE_EXHAUSTED`, `ABORTED`; the shipped set is `UNAVAILABLE`, `ABORTED`, since the 2026-08-18 retry-safety entry above removed `RESOURCE_EXHAUSTED` and gave the seven mutation RPCs a `retryPolicy`-less `methodConfig` entry of their own. Up to 3 retries (4 total attempts), 100ms initial backoff, 2x multiplier, 5s max backoff. No public API change; callers can still override via `WithDialOptions`.
- **2026-08-14**: RPC and stream errors are now mapped to native `*client.Error` values inspectable via `errors.Is`/`errors.As`, instead of raw `%w`-wrapped gRPC status errors. New sentinels: `client.ErrNotFound`, `client.ErrAlreadyExists`, `client.ErrInvalidArgument`, `client.ErrFailedPrecondition`, `client.ErrPermissionDenied`, `client.ErrUnauthenticated`. Applies to every RPC call and every `iter.Seq2` streaming iterator (`ReadRelationships`, `LookupResources`, `LookupSubjects`, `ExportRelationships`, `Updates`), so mid-stream errors are native too. `errors.Unwrap` still exposes the underlying gRPC status error for advanced inspection.
- **2026-08-14**: Per-item errors from `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (surfaced via `BulkCheckPermissions` response pairs) are now mapped to native `*client.Error` values through the same `mapGRPCError` path as top-level RPC errors, instead of being string-formatted. `errors.Is(err, client.ErrInvalidArgument)` (and the other sentinels) now works for per-item bulk-check failures, not just top-level RPC failures.

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Go SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic Go client.
  - `consistency` package: `Full()`, `MinLatency()`, `AtLeast()`, `Snapshot()` strategy constructors
  - `rel` package: `Relationship` struct, `Interface` trait, `FromTriple`/`MustFromTriple`/`FromTuple`/`FromObjects` constructors, `WithCaveat`/`WithExpiration` modifiers, `Filter` builder, `Txn` transaction builder with `Create`/`Touch`/`Delete`/`MustNotMatch`/`MustMatch`, `Update` type for watch events
  - `client` package: `NewPlaintext`/`NewSystemTLS`/`NewWithOpts` constructors, `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (all via BulkCheckPermissions), `Write`/`ReadRelationships`/`DeleteRelationships`, `LookupResources`/`LookupSubjects`, `ReadSchema`/`WriteSchema`, `Updates` (watch)
  - Examples: `check_permission`, `write_relationships`, `read_relationships`, `lookup_resources`, `lookup_subjects`, `watch_changes`, `schema_management`, `bulk_operations`
- **2026-03-16**: Added missing API methods for full non-deprecated coverage.
  - `client` package: `ReflectSchema`, `ComputablePermissions`, `DependentRelations`, `DiffSchema` (schema reflection), `ExpandPermissionTree`, `ImportRelationships`, `ExportRelationships` (bulk import/export), `RegisterRelationshipCounter`, `CountRelationships`, `UnregisterRelationshipCounter` (experimental counters)
  - New types: `SchemaDefinition`, `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`, `SchemaCaveatParameter`, `ReflectSchemaResult`, `RelationReference`, `SchemaDiff`, `ExpandResult`, `CountResult`
  - Examples: `schema_reflection`, `relationship_counters`
- **2026-03-16**: Added transparent cursor-based pagination, batching, and sentinel errors.
  - `ReadRelationships`, `LookupResources`, `ExportRelationships` now auto-paginate with internal cursors (512-item pages); `LookupSubjects` uses a single streaming call (no cursor support in SpiceDB yet)
  - `DeleteRelationships` auto-pages in 10,000-item batches until all matching rels deleted
  - `CheckIter` now batches input relationships in chunks of 1,000 (instead of collecting all first)
  - `rel` package: added sentinel errors `ErrInvalidResource`, `ErrInvalidRelation`, `ErrInvalidSubject`
