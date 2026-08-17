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
