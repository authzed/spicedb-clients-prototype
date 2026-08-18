# SpiceDB Client Libraries — Idiomatic Design Vision

## Philosophy

SpiceDB clients should feel native to each language. Users should not need to
know about protobuf or gRPC to use them. The client API should guide users
toward correct, performant usage patterns ("pit of success").

## Backwards Compatibility Mandate

**Public API surfaces are append-only, unless otherwise specified.**

- Never remove methods or types from the public API, unless removed from the underlying protocol buffer API
- Never change method signatures, unless forward compatible
- Deprecate with new alternatives — add new methods instead of changing existing ones, unless existing methods can be changed in a forward-compatible manner
- This applies even when idiomatic improvements suggest changes, unless explicitly specified to be a `BREAKING CHANGE`. If so, then that specification tests precedence, confirmation must be received from the runner before the changes are made, and the breaking change should be documented in the release notes
  for each client, with at least one example of how to migrate forward
- Examples must _ALWAYS_ compile and pass testing, unless the user has _EXPLICITLY_ approved the breaking change

## Deprecation Propagation

When a method is deprecated in the proto/gRPC definitions, it MUST be marked as
deprecated in both the proto client and the idiomatic client.

Language-appropriate deprecation mechanisms:

- **Go**: `// Deprecated: use XYZ instead.` comment on the function/method
- **Python**: `@deprecated` decorator (Python 3.13+) or `warnings.warn(..., DeprecationWarning)`
- **TypeScript**: `@deprecated` JSDoc tag
- **C#**: `[Obsolete("Use XYZ instead")]` attribute
- **Java**: `@Deprecated` annotation + `@deprecated` Javadoc tag
- **Ruby**: `warn "[DEPRECATION] ..."` in method body
- **Rust**: `#[deprecated(note = "Use XYZ instead")]` attribute

Deprecated methods must remain functional until removed from the proto definitions.

## API Coverage

Idiomatic clients MUST expose all non-deprecated APIs from the proto layer in
some idiomatic form. No proto API should be silently omitted — if it exists in
the proto client, users must be able to access it through the idiomatic client.

APIs from the `ExperimentalService` (or any service/method marked experimental
in the proto definitions) must be clearly marked as experimental in the
idiomatic client using language-appropriate mechanisms (Go: `// Experimental:`
comment, Python: docstring note, TypeScript: `@experimental` JSDoc tag). This
signals to users that the API may change without following the backwards
compatibility mandate.

## Common Idioms (All Languages)

All idiomatic clients should provide:

1. **Simple constructors** with sensible defaults — security posture should be
   obvious from the constructor name (e.g., plaintext vs TLS)
2. **Native error types** — not raw gRPC status codes. Errors should be
   inspectable using language-native patterns (Go: sentinel errors + errors.Is,
   Python: exception hierarchy, TypeScript: typed error classes)
3. **Iterator/async patterns** for streaming RPCs — use the most natural
   mechanism for the language (Go: iter.Seq2, Python: async iterators in
   `spicedb.aio` and plain iterators in `spicedb.sync`, TypeScript:
   AsyncIterableIterator)
4. **Builder or options patterns** for complex requests — users should not need
   to construct nested proto messages
5. **First-class ZedToken support** — consistency tokens should be opaque
   (strings, not proto types) and required as explicit parameters, never
   silently defaulted
6. **Automatic retry** with exponential backoff for transient gRPC errors
7. **Consistency helper** should be provided to take in a ZedToken and, if
   present use at least as fresh and, if not present (i.e. its empty), return
   full consistency instead. Another should be provided to return minimize
   latency instead. Both should have descriptive names.

## RULE: A system-TLS constructor must reach a real server

A client's default secure constructor — `new_system_tls`, `NewClientWithSystemCerts`,
or whatever each language calls it — must use the **platform trust store**, and must be
covered by a test that **completes a real TLS handshake**.

1. The platform store is the contract. A constructor named for system trust that
   compiles in its own fixed root set is not using system trust, and will not honour a
   CA an operator installed on the host.
2. A test that asserts a connection *fails* does not satisfy this rule. An unreachable
   host, a DNS error, and an empty trust store are indistinguishable to such a test, so
   it passes for the wrong reason while reading as TLS coverage. Assert success against
   a reachable endpoint.
3. Where the handshake test needs the network, gate it behind an environment variable
   and run it in a CI job that has network access. Gating is acceptable; absence is not.

A client whose default secure constructor cannot reach a public endpoint is not
shippable, and no amount of green CI substitutes for one honest handshake.

## RULE: Only an unconditional grant is true

**Binding on every client, in every language, with no exceptions.**

`CheckPermissionResponse.permissionship` has three non-error values:
`NO_PERMISSION`, `HAS_PERMISSION`, and `CONDITIONAL_PERMISSION`. A
`CONDITIONAL_PERMISSION` means the server found a matching relationship but could
**not** evaluate its caveat, because the required context was not supplied. The
server is saying "I need more information" — it is **not** saying yes.

Therefore:

1. **Only `HAS_PERMISSION` may ever be treated as a grant.** `CONDITIONAL_PERMISSION`,
   `NO_PERMISSION`, and `UNSPECIFIED` are all not-a-grant. An unrecognized future
   enum value is also not-a-grant.
2. **Every predicate that answers "does this subject have the permission" —
   `HasPermission()`, `has_permission`, `has_permission?`, `hasPermission()` — must
   return true for `HAS_PERMISSION` and false for everything else.** A single
   equality comparison, never a disjunction.
3. **Aggregate predicates count only `HAS_PERMISSION`.** `check_any` returns true
   only if some result is an unconditional grant; `check_all` only if every result
   is. A conditional never contributes to a true.
4. **Where a language can make a check result behave as a boolean, it must yield
   the same answer.** Python's `CheckResult.__bool__` returns `has_permission`, so
   `if result:` is safe.
5. **Where a language cannot** — Ruby (every object but `nil`/`false` is truthy) and
   TypeScript (objects are unconditionally truthy), with no override available —
   **no documentation, docstring, README, or example in that client may show a
   check result used directly as a condition.** Every sample must go through the
   predicate. This is a hard requirement, not a style preference: in those clients a
   bare `if result` silently grants on an unevaluated caveat, and the docs are the
   only thing standing between a reader and that bug. Go, Rust, C#, and Java are
   safe by construction — `if result` does not compile.
6. **The third state must remain reachable.** Collapsing the check surface to a
   bare boolean is forbidden, because it makes "denied" indistinguishable from
   "you did not supply the caveat context." Callers must be able to tell those
   apart, and to learn which context was missing.

The failure this rule exists to prevent is real and shipped: one client previously
returned `true` for `CONDITIONAL_PERMISSION` by design, granting access on a caveat
that was never evaluated.

## The Check Surface: a three-valued result, not a boolean

**Binding on every client.** The RULE above says what a check *means*; this section
says what a check *returns*. Per-client `DESIGN.md`s inherit from here — they name
the fields in their own idiom, they do not redefine the contract.

Both check methods — the singular (`check_permission`) and the plural
(`check_permissions`) — return a `CheckResult`, never a bare `bool`. `CheckResult`
carries exactly three pieces of state:

| Concept | Contract |
|---|---|
| `permissionship` | The server's answer, from the four-valued shared `Permissionship` enum: unspecified, no-permission, has-permission, conditional-permission. Unrecognized future wire values map to *unspecified*, which is not a grant. |
| `missing_context` | The caveat context keys the server needed and did not receive, from `CheckPermissionResponse.partial_caveat_info.missing_required_context`. Empty unless the result is conditional. It must carry the server's actual key names — a bare "something was missing" flag does not satisfy this. |
| `checked_at` | The `ZedToken` from `CheckPermissionResponse.checked_at`. Threading it into the client's `at_least`-equivalent consistency strategy is how a caller gets read-your-writes through the public API. |

Plus the predicate (`has_permission`), governed by the RULE.

Invariants that hold in all seven clients:

1. **One `Permissionship` type serves both check and lookup.** A caller learns the
   concept once. Lookups never yield *no-permission* (a non-matching pair is simply
   absent from the stream); *no-permission* only appears on a check, where "no" is
   itself an answer. Where a language exposes the enum's ordinal, the value is an
   implementation detail: no client serializes it, compares it ordinally, or relies
   on its ordering. Every client maps to and from the wire explicitly, so the native
   ordinals may differ between clients and from the proto's, harmlessly and by
   design.
2. **`checked_at` on a bulk check is response-level, not per-item.**
   `CheckBulkPermissionsResponseItem` has no token of its own, so the one token on
   the enclosing response is propagated onto every result in the batch.
3. **`check_any` / `check_all` stay boolean and count only an unconditional grant**
   (RULE clause 3). Callers needing the third state use the plural method.
4. **A per-item error in a bulk check is raised as a typed error**, never coerced
   into a falsy or unspecified result — otherwise permission-denied, invalid-argument,
   and a real "no" become indistinguishable.
5. **Reads and writes yield a revision too.** The write surface returns the revision
   it committed at wherever the proto carries one, and every yielded lookup item
   carries `looked_up_at` (identical for every item in one stream — it is a property
   of the call, not the item). Bulk import is the one exception, because
   `ImportBulkRelationshipsResponse` has no token on the wire at all.

## Caveat Context on the Check Surface

**Binding on every client.** `missing_context` names the keys the server needed;
this is the API that supplies them. Without it the diagnostic would not be
actionable.

Every client accepts caveat context on a check in **both** forms:

- **Call-level** — a default applied to every relationship in the call.
- **Per-item** — attached to one relationship, overriding the call-level default
  for that relationship only.

**Merge rule: key-level, item wins.** For each item the client sends
`{...call_level, ...item_level}`. The item's keys win on conflict; call-level keys
the item does not mention are **retained**. Wholesale replacement is forbidden — an
item supplying one key would silently drop every shared key, and the caveat would
then fail for missing context, landing the caller back in the conditional state this
work exists to make legible.

```
call-level:  {now: 42, region: "us"}
item-level:  {region: "eu"}
sent for that item: {now: 42, region: "eu"}
```

An item that supplies no context inherits the call-level context unchanged. When
neither is supplied, **no `context` field is set on the wire at all** — not an empty
struct.

Two further requirements:

- **Check-time context is not write-time caveat context.** The context attached to a
  relationship on write (`with_caveat` and friends, stored in
  `Relationship.optional_caveat.context`) and the context supplied at check time
  (`CheckPermissionRequest.context` field 5 /
  `CheckBulkPermissionsRequestItem.context` field 4) are different wire fields with
  different lifetimes. A client must keep them as separate concepts and must never
  send one where the other belongs.
- **Types are preserved on the check path.** Caveat context values go onto
  `google.protobuf.Value`'s `kind` oneof by type — numbers as numbers, booleans as
  booleans, null as null, nested maps/lists recursively. Stringifying a numeric
  parameter makes a caveat like `now < 100` fail to evaluate, and it fails *quietly*,
  as another conditional result.

`check_any` / `check_all` accept the same context shape as `check_permissions` — they
aggregate over the same request and must be able to evaluate caveats.

## What NOT To Do

- No auto-generated feeling — the API should read like hand-written code
- No protobuf types in the public API — wrap them in language-native types
- No requiring users to construct nested proto messages
- No silent defaults for consistency — make users choose explicitly
- No exposing gRPC internals (channels, stubs, metadata) in the primary API
  (escape hatches for advanced use are acceptable as clearly marked secondary API)
- **No treating a conditional permission as a grant** — see the RULE above

## CI Workflow Conventions

The repo uses one GitHub Actions workflow file per language plus `spicedb-gen.yaml` and `meta.yaml`. Rules for maintaining and extending CI:

1. **Per-language file.** Every language has its own `.github/workflows/<lang>.yaml`. Do not add language-specific jobs to a shared workflow. Cross-cutting checks (e.g., `mage gen:all` nodiff, YAML lint) live in `meta.yaml`.

2. **Standard job set per language workflow.** Each language workflow contains: `paths-filter`, `lint`, `unit`, `integration`, and `apicompat` (if the language appears in `apiCompatLanguages` in the root `Magefile.go`). All four work-jobs gate on `paths-filter.outputs.changed == 'true'`. `apicompat` additionally gates on `github.event_name == 'pull_request'` so it has a base ref to diff against.

3. **paths-filter scope.** Each filter must watch the workflow file itself, the language's `proto-clients/spicedb-<lang>-proto/**`, the language's `spicedb-<lang>/**`, root `Magefile.go`, and root `go.mod` / `go.sum`. Workflow edits without filter coverage produce a silently-skipping CI.

4. **Runner sizing.** Default to `depot-ubuntu-24.04-arm-4`. Use `arm-small` for trivial steps (paths-filter, apicompat, yaml lint). Use `arm-8` only for cross-cutting work that touches every language (e.g. `meta.gen-nodiff`). Justify any size deviation in a comment.

5. **Action pinning.** Third-party actions (anything outside `actions/*` and `authzed/*`) pin to commit SHA with `# vX.Y.Z` comment. Dependabot's `github-actions` ecosystem keeps these current.

6. **Integration tests.** Each `mage <lang> integrationTest` target is self-contained — it starts its own docker-compose, runs tests, tears down. CI runs at most one `integrationTest` per job (they all bind `localhost:50051`). Cross-language parallelism is fine because each runner is isolated.

7. **Adding a new language.** Checklist:
   - Create `.github/workflows/<lang>.yaml` from an existing one (Go is a good template if compiled, Python if interpreted).
   - Add the language's directories to `.github/dependabot.yml`.
   - Add a per-language Quick Start in `README.md` (typed if `spicedb-gen` supports it, idiomatic otherwise).
   - Add the language to `languages` (and `apiCompatLanguages` if applicable) in the root `Magefile.go`.

8. **Adding a new job type.** If you find yourself wanting a new kind of check (e.g., security scanning, license check), add it to **every** language workflow at once, document it here, and add a paths-filter clause if it should be skippable.
