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
- Examples must *ALWAYS* compile and pass testing, unless the user has *EXPLICITLY* approved the breaking change

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

A client's default secure constructor — Go `NewSystemTLS`, Rust and Ruby
`new_system_tls`, Java `createSystemTls`, C# `CreateSystemTls`, or whatever else a
language calls it — must use its ecosystem's **default trust source**, and must be
covered by a test that **completes a real TLS handshake**.

1. **The client library does not supply the roots.** The constructor must take its
   trust anchors from the platform store or from whatever its language runtime or gRPC
   implementation uses by default. Delegating to that default *is* system TLS in that
   ecosystem: Go `credentials.NewTLS(nil)`, C# `ChannelCredentials.SecureSsl`, Python
   `grpc.ssl_channel_credentials()`, Ruby `GRPC::Core::ChannelCredentials.new`, Java
   `useTransportSecurity()`, connect-node's default in TypeScript, and tonic's
   `.with_native_roots()` in Rust all satisfy this clause. What is prohibited is the
   *client library* vendoring, embedding, compiling in, or otherwise selecting its own
   root-certificate set in place of that default — including selecting the empty set,
   which is the defect this clause exists to catch: Rust handed tonic a config with no
   trust anchors at all, so no handshake could ever succeed.

   The genuine hazard this leaves visible: not every runtime default *is* the OS store.
   gRPC's C-core (Python's `grpcio`, Ruby's `grpc`) compiles in its own `roots.pem`, and
   Node ships a bundled Mozilla store, so on those clients a CA an operator installed on
   the host is not honoured. That is a property of the ecosystem, not a violation of
   this clause. Giving callers a way to supply their own CA bundle is the remedy, and it
   is not optional where the hazard applies:

   **A client whose default trust source is not one the operator can write to MUST let a
   caller supply their own.** For those clients — Python and Ruby on gRPC's C-core,
   TypeScript on Node's bundled store — the delegation this clause permits is conditional
   on that override existing: without it a private-CA deployment is unreachable with no
   supported workaround, which is a defect under this clause rather than a gap outside
   it. Where the default *is* an operator-writable store — the OS store for Go, C# and
   Rust, the JDK's `cacerts` for Java — installing the CA on the host already serves that
   case through the default constructor, so this clause requires nothing further. An
   override is still worth having there (chiefly for mutual TLS, and for pinning a CA the
   host does not trust), but its absence is not a violation of this rule. Scope the "must"
   that way and the enumeration below is consistent with it; read it as universal and it
   is not.

   Where each client's escape hatch lives, since they are not uniform and three of them
   are not new API at all:

   - **Go** — `WithDialOptions(grpc.WithTransportCredentials(...))`. Later dial options
     overwrite earlier ones, so a caller's credentials win over the client's default.
   - **C#** — `CreateFromChannel`, which takes a caller-built `GrpcChannel` and therefore
     any `HttpClientHandler`/`SocketsHttpHandler` TLS configuration.
   - **Java** — `ClientOption.apply(ManagedChannelBuilder)`. `grpc-netty-shaded` is
     declared `api` in `proto-clients/spicedb-java-proto/build.gradle.kts`, so it reaches
     consumers transitively and the builder is usable from their code.
   - **Python** — `ca_cert=`, `client_cert=`, `client_key=` (PEM bytes) on both
     `spicedb.sync.SpiceDBClient` and `spicedb.aio.SpiceDBClient`.
   - **TypeScript** — `tls: { caCert, clientCert, clientKey }` on `SpiceDBClientOptions`
     and `createSpiceDBClient`, threaded to `Http2SessionManager`'s session options.
   - **Ruby** — `SpiceDB::Client.new_custom_tls(endpoint, token, ca_cert:, client_cert:,
     client_key:)`.
   - **Rust** — none. Not required by the clause above, since tonic's `tls-native-roots`
     reads the OS trust store at runtime and an operator-installed CA is therefore already
     honoured by `new_system_tls`. Two gaps remain and are stated plainly in
     `spicedb-rust/DESIGN.md` rather than papered over: an image with no OS trust store at
     all, and **mutual TLS, which this client cannot do at all** — reading the host's
     roots says nothing about presenting a client certificate, so `tls-native-roots` does
     not cover it.

   Supplying trust material is a TLS concern and must never double as a transport switch.
   A client must refuse the combination of custom trust material and an insecure
   transport rather than silently discarding the material, and the escape hatch must not
   become a construction path that skips the guard in **RULE: Credentials over insecure
   transport require an explicit opt-in**.

   This clause is enforced by code review, not by the handshake test: a connection to a
   public endpoint succeeds under almost any plausible root set, so the test cannot tell
   a delegated default from a vendored bundle. What the test does catch — and must keep
   catching — is the empty trust store.
2. **The test must complete a handshake.** A test that never gets as far as a TLS
   handshake proves nothing, whichever way it asserts. Asserting a connection *fails*
   cannot distinguish an unreachable host, a DNS error, and an empty trust store, so it
   passes for the wrong reason while reading as TLS coverage. Asserting a constructor
   *succeeds* is no better wherever the language connects lazily — Ruby's channel is
   built without dialling, so `new_system_tls('grpc.example.com:443', …)` "passes"
   without a packet leaving the process. Assert a completed handshake against a
   reachable endpoint; where the constructor is lazy, force the connection inside the
   test (an RPC, or the language's explicit connect) so the assertion cannot be
   satisfied without one.
3. Where the handshake test needs the network, gate it behind an environment variable
   and run it in a CI job that has network access. Gating is acceptable; absence is not.
   The CI step must fail if the test did not actually run — a bare name filter that
   matches nothing exits 0 in most test runners, which reproduces this rule's own
   failure mode one layer up.

A client whose default secure constructor cannot reach a public endpoint is not
shippable, and no amount of green CI substitutes for one honest handshake.

## RULE: Credentials over insecure transport require an explicit opt-in

All seven clients send their bearer token to any host over plaintext with no host check
whatsoever. An exhaustive search for `127.0.0.1`, `loopback`, and `localhost` across every
client and every proto tier turned up only doc-comment samples — zero runtime conditionals
anywhere.

Three clients go further: each contains code written specifically to bypass its transport's
own refusal to attach call credentials to an insecure channel, with a comment explaining why.

- **Go** — a `PerRPCCredentials` implementation whose `RequireTransportSecurity()` returns
  `!insecure`, so grpc-go's check passes exactly when the connection is insecure.
- **C#** — a `CallInvoker.Intercept` that sets the header raw, with the comment *"since
  CompositeChannelCredentials requires secure transport"*.
- **Ruby** — a `BearerTokenInterceptor` merging metadata directly, with the comment *"channel
  credentials can't carry call credentials over a plaintext channel"* — grpc-ruby's C-core
  actually raises on the composed path, so the bypass avoids a hard failure.

Python, Java, TypeScript, and Rust never engage a checked mechanism at all: plain metadata or
headers is the only path their underlying libraries offer for an insecure channel, so there is
no refusal for them to defeat — the guard was simply never built. gRPC's refusal to attach call
credentials to an insecure channel exists precisely to prevent this. Routing around it —
deliberately, as in the three clients above, or by omission, as in the other four — is what
this rule now governs.

1. **A client MUST NOT send credentials over an insecure transport to a non-loopback host
   unless the caller has explicitly opted in through a named parameter.** Named means a
   reader cannot mistake it for a default: a distinct, documented option the caller supplies
   on purpose, never a boolean that does double duty as the plaintext-transport switch.
2. **A warning is not sufficient.** A log line the developer never reads does not close a
   credential leak. The client must refuse, or require the explicit opt-in above, before it
   proceeds — logging while sending the credential anyway does not satisfy this rule.
3. **The official Python client sets the precedent.** Its insecure posture lives on a
   separately-named `InsecureClient` a caller must reach for deliberately, not a flag on the
   default constructor. A client whose only route to insecure operation is a boolean on its
   primary entry point does not meet this bar.

Name the failure this rule exists to prevent: a developer copies `insecure: true` from a
localhost example into a staging config, and a long-lived SpiceDB token — a complete
authorization bypass in anyone else's hands — goes onto the wire in cleartext with nothing
signalling that it happened.

**The guard's answer must be the transport's answer.** Whether an endpoint counts as loopback
is decided by asking the same parser the client dials with — `System.Uri` for Grpc.Net.Client,
`http::Uri` for tonic, WHATWG `URL` for Connect-ES over Node http2, `URI.create("//" + name)`
for grpc-java's `DnsNameResolver`, grpc-go's own target resolution — not by a hand-rolled
string split. A split that disagrees with the transport anywhere is a bypass: given
`127.0.0.1:443@evil.com`, a last-colon split reads the host as `127.0.0.1` while a URI parser
reads `127.0.0.1:443` as *userinfo* and connects to `evil.com`. Where a binding genuinely cannot
reach its transport's parser (Python and Ruby hand the target to grpc's C-core, which parses it
in C++), the client must say so explicitly and fail closed instead: refuse any endpoint
containing a character that could move the authority under URI parsing — `@`, `/`, `?`, `#`,
whitespace.

A consequence, and it is intended: **the precise set of loopback spellings is per-client, and
uniformity across clients is explicitly not promised.** Parsers differ in what they normalize —
IPv4-mapped IPv6, bracketed hostnames, percent-encoding — so `::ffff:127.0.0.1` or
`[localhost]:50051` may be loopback in one client and not another. That is correct. Forcing the
seven to agree would mean writing normalization the parsers themselves do not do, which is the
hand-rolled string manipulation this rule exists to remove. What every client must guarantee is
the security property, not the spelling: an endpoint the guard calls loopback is one the
transport dials on loopback, and everything else takes the named opt-in. Divergences that
fail closed are acceptable; a divergence that fails open is the bug.

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
7. **An aggregate over zero checks is not a grant.** `check_all` (Python, Ruby, Rust),
   `CheckAll`/`CheckAllWithContext` (Go), `checkAll` (TypeScript, Java), and
   `CheckAllAsync`/`CheckAllWithContextAsync` (C#) MUST return false for an empty input set.
   Every language's `all`/`every` primitive is vacuously true on an empty sequence, so the
   idiomatic implementation is the bug: all seven clients had this defect independently. Guard
   the empty case explicitly, before the aggregate, and test it. `check_any` is already correctly
   false on empty — that asymmetry is intentional and must not be "made consistent."

The failure this rule exists to prevent is real and shipped: one client previously
returned `true` for `CONDITIONAL_PERMISSION` by design, granting access on a caveat
that was never evaluated.

## The Check Surface: a three-valued result, not a boolean

**Binding on every client.** The **RULE: Only an unconditional grant is true**, above, says what
a check *means*; this section says what a check *returns*. Per-client `DESIGN.md`s inherit from
here — they name the fields in their own idiom, they do not redefine the contract.

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

## Caveat Context on the Check and Write Surfaces

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
- **Types are preserved on every caveat-context path.** Caveat context values go onto
  `google.protobuf.Value`'s `kind` oneof by type — numbers as numbers, booleans as booleans,
  null as null, nested maps and lists recursively — on the check path (`CheckPermissionRequest.context`,
  `CheckBulkPermissionsRequestItem.context`) **and** on the write path
  (`Relationship.optional_caveat.context`), in both directions. Stringifying a numeric parameter
  makes a caveat like `now < 100` fail to evaluate, and it fails *quietly*, as another
  conditional result. Write-time is the worse half: a bad check context fails one call, while a
  bad write context is persisted and mis-evaluates every future check against that
  relationship — re-checking never repairs it, only rewriting does.

`check_any` / `check_all` accept the same context shape as `check_permissions` — they
aggregate over the same request and must be able to evaluate caveats.

## RULE: A conversion that cannot preserve meaning must fail

Dropping a value, stringifying it, or widening a filter are all silent-wrong-answer machines: the
call succeeds, the caller proceeds, and the damage surfaces later somewhere else. But the correct
response to an unrepresentable value is not one behavior — it depends on **who supplied it**:

1. **Caller-supplied data the client cannot represent MUST raise a typed error naming what could
   not be converted.** Caveat context that will not convert, and a filter whose constraint the
   wire format cannot express, are both requests the caller made. The caller can see the failure
   and fix their input, so the client does not approximate the value and does not discard it — it
   fails loudly instead of guessing.
2. **Server-supplied values the client does not recognise MUST NOT raise, and MUST map to the
   safe, non-permissive default — never a grant, and never a write.** A `permissionship` value
   added to the wire after a client shipped is not the caller's mistake, and the caller has no
   input to correct. Raising here would break forward compatibility: a server rolling out a new
   enum value would make every deployed client throw on every check. This is the same posture
   already stated for the grant path — *RULE: Only an unconditional grant is true*, clause 1
   ("An unrecognized future enum value is also not-a-grant") and the `permissionship` row of the
   Check Surface table ("Unrecognized future wire values map to *unspecified*, which is not a
   grant") — restated here as the general rule for any server-originated enum, not only
   `permissionship`.

The two clauses are not in tension: one governs data the caller can fix by raising loudly, the
other governs data the caller cannot fix by degrading safely. Confusing the two directions is the
failure mode either way — raising on an unrecognised server enum turns a routine server upgrade
into a client-side outage, and silently discarding unrepresentable caller data turns a caller's
mistake into a silent wrong answer.

## RULE: Error mapping must not lose the server's detail

`OUT_OF_RANGE` is mapped to a typed error in 0 of 7 clients. `UNAUTHENTICATED` is mapped in
only one — Go; the other six leave it indistinguishable from an internal server fault. Every
proto tier already generates SpiceDB's `ErrorReason` enum from `error_reason.proto`, and not
one idiomatic client references it anywhere. Six clients do preserve the underlying error
object — as `cause`, `innerException`, or via `Unwrap` — so the information is reachable but
unparsed. Rust discards it outright: `from_grpc_status(code: i32, message: String)` reduces the
status to a code and a string before mapping ever runs, a lossy boundary its own doc comment
admits.

1. **A client MUST map `OUT_OF_RANGE` and `UNAUTHENTICATED` to typed errors**, not left to fall
   through to a generic status-code wrapper. These are not exotic codes: they are the two a
   caller actually hits in production, and every other mapped code in a client's error
   hierarchy already sets the precedent for treating them the same way.
2. **A client MUST preserve the underlying status object on every typed error**, so
   `google.rpc.Status`'s details and SpiceDB's `ErrorReason` remain reachable to a caller who
   wants them — as `cause`, `inner`, `Unwrap()`, or the language's equivalent. A mapping step
   that reduces a status to a bare code and string, as Rust's does, has already thrown the
   information away before it can be used; parsing must work against the original status,
   never against a string rebuilt from it.

Name both consequences:

- `OUT_OF_RANGE` is SpiceDB's code for an expired or garbage-collected ZedToken. It is the
  single most actionable recoverable error in a token-threading application, and its correct
  handling is mechanical — discard the stale token, re-read at full consistency, retry.
  Collapsed into a generic error, every caller must string-match a message to recover an error
  the client already knew the shape of.
- `UNAUTHENTICATED` is the most common error a new integration produces — a wrong, expired, or
  rotated API token. In six clients it is currently indistinguishable from an internal server
  fault, so a caller cannot write "refresh credentials on auth failure, page someone on
  internal error" — the one distinction that error most needs to carry.

## RULE: Automatic retry is for idempotent operations only

A client MUST NOT silently retry a mutation whose replay changes the outcome. A
`WriteRelationships` containing `OPERATION_CREATE`, or any request carrying preconditions, is
not idempotent: if it commits and the response is lost, the retry returns `ALREADY_EXISTS` or
`FAILED_PRECONDITION` and the caller concludes a write failed that in fact succeeded. Retry
reads freely; retry mutations only when the caller opts in.

`RESOURCE_EXHAUSTED` MUST NOT be in the retryable set. In SpiceDB it signals memory load-shed or
a deterministic `MaxDepthExceeded` — the first is made worse by retrying and the second can
never succeed.

Backoff MUST be jittered. Without jitter every client in a fleet retries on the same schedule
after a restart, converting a recovery into a thundering herd.

## RULE: A unary call must have a deadline

A wedged SpiceDB — one that accepts the connection but never answers — hangs every caller that
has no bound on the call. It hangs them silently: the connection is open and nothing looks wrong
at the transport level, so no error is ever produced, and automatic retry cannot help because
there is nothing to retry against. Callers queue up behind the wedged call until the connection
pool is exhausted, and an outage that started with one bad call spreads to requests that have
nothing to do with it.

1. **A client MUST let a caller bound a unary call.** Every unary RPC — `CheckPermission`,
   `WriteRelationships`, `ReadSchema`, and the rest — must accept a deadline or timeout from the
   caller, expressed in the language's idiomatic form: a context deadline in Go, a timeout
   parameter or cancellation token elsewhere.
2. **A client SHOULD apply a default when the caller supplies none.** An opt-in-only parameter
   leaves the defect intact for everyone who does not opt in — in practice, most callers. The
   default must be finite; treating "no timeout" as the default reproduces the defect this rule
   exists to close. `authzed-node` sets the precedent: it ships a `DEFAULT_DEADLINE_MS`, with a
   comment citing `grpc/grpc-node#541`, a known gRPC failure mode the default exists to guard
   against.
3. **Streaming calls MUST NOT inherit the unary default.** This excludes every RPC that isn't
   plain unary — server-streaming (`watch`, `export`, and the lookup streams: `LookupResources`,
   `LookupSubjects`, and their variants), client-streaming (`ImportBulkRelationships`), and any
   future bidirectional RPC alike. The rule is about call *shape*, not which end does the
   streaming: `watch` is long-lived because it waits on the server for as long as the process
   cares to keep watching; `ImportBulkRelationships` is long-lived because its duration scales
   with the size of the dataset the *caller* is feeding it, and a caller legitimately streaming in
   millions of relationships can take minutes to do so. No fixed default is correct for either
   shape, and applying one would make the transfer itself the outage — the caller's data volume,
   not a wedged server, would be what trips it.

   What replaces the default differs by which end is streaming, and the difference is not
   cosmetic:

   - **Server-streaming calls MUST offer cancellation, not a deadline.** A deadline answers "how
     long may this take?", and for `watch` the honest answer is "as long as the process cares to
     keep watching" — there is no duration that is wrong to allow and no duration whose expiry
     means something went wrong. The question a caller of a long-lived stream actually needs to
     answer is "I am done with this, stop" — which is cancellation, and which is a *caller*
     decision made at an arbitrary later moment rather than a bound guessed up front. So these
     calls take no per-call timeout, and instead satisfy **RULE: Abandoning a stream must release
     it**, below. Offering a deadline here instead would be worse than offering nothing: whatever
     value a caller picked would eventually fire mid-stream and look like a server fault.
   - **Client-streaming calls SHOULD keep an explicit per-call override.**
     `ImportBulkRelationships` is the caller's own upload, so the caller *does* know roughly how
     long their dataset should take and may legitimately want to cap it. That override must be
     opt-in and default to unbounded, since only the caller knows the volume.

   A caller who needs a hard wall-clock bound on a server stream still has one — cancel it from a
   timer. The point is that the client must not pick that timer's value for them.

A client with no deadline anywhere is one wedged server away from an outage that looks, from the
caller's side, exactly like a hang with no cause.

**On worst-case latency**: what a timeout of `t` bounds depends on where the retry loop lives, and
the two shapes in this repo differ by more than 4x on the same nominal value. A client MUST
document which shape it has; a caller reasoning about an end-to-end budget cannot infer it from the
parameter name.

- **Per-attempt budget** (six clients: Python, TypeScript, Java, C#, Ruby, Rust). The client hand-
  rolls its retry loop and applies `t` fresh to each attempt, deliberately: a budget that shrank
  across attempts would make a call that legitimately needs several retries *more* likely to fail
  than one that needs none. So `t` bounds each attempt, not the call — worst-case latency is
  `t × (retries + 1)` plus backoff between them, and for an auto-paging call (e.g. a filtered
  delete) the same `t` applies fresh to each page.
- **Absolute deadline** (Go). Retry is grpc-go's own service-config `retryPolicy`, which reuses the
  caller's `context.Context` across every attempt. A context deadline is a point in time, not a
  duration, so it bounds the whole operation: attempts, backoff, and pagination all draw down the
  same budget, and a retry that starts after the deadline has passed fails immediately.

Concretely, `t = 30s` on a read that exhausts its retries means up to ~120 s plus backoff in the
six, and at most 30 s in Go. Neither is wrong — Go's is what a `context.Context` *means*, and
reimplementing per-attempt budgets on top of it would fight the language's central convention —
but a caller porting a timeout value between clients is not carrying its meaning with it. In the
per-attempt clients, a caller who needs a true end-to-end bound must impose it themselves, above
the client.

## RULE: Abandoning a stream must release it

Streaming RPCs invite a common idiom: take the first N results and stop. If stopping early does
not tell the server to stop, the client has traded a fast return for a leak — the server keeps a
dispatch open per abandoned stream, for as long as the underlying connection lives, because
nothing ever told it the caller walked away.

1. **A client MUST expose cancellation for every streaming call.** The caller-facing surface — an
   async iterator, a generator, a channel — must offer an explicit, working way to stop consuming
   before the stream is exhausted.
2. **The transport MUST actually release the stream on abandonment.** Exposing a cancel method
   that the transport underneath ignores does not satisfy this rule; the caller believes the
   stream is closed and it is not. Verify this against the transport the client actually ships,
   not the one implied by its API.
3. **A `for`-loop `break` calling a generator's `return()` is not sufficient on its own.** Where a
   language's iteration protocol closes a generator by calling `return()` on `break`, that
   mechanism only releases the stream if the transport underneath honors it — and this is a trap
   precisely because the call site looks identical whether it does or not. Connect-ES is a
   concrete instance: it deliberately omits `return()` on its iterator, so a `break` in consuming
   code never releases the underlying HTTP/2 stream. A client built on a transport with this
   property must wire cancellation through an explicit path and must not rely on the loop idiom
   alone.

A client that cannot cancel an in-flight stream leaks a server-side dispatch per abandoned call —
silently, since the leak is invisible from the caller's side of a `break`.

## What NOT To Do

- No auto-generated feeling — the API should read like hand-written code
- No protobuf types in the public API — wrap them in language-native types
- No requiring users to construct nested proto messages
- No silent defaults for consistency — make users choose explicitly
- No exposing gRPC internals (channels, stubs, metadata) in the primary API
  (escape hatches for advanced use are acceptable as clearly marked secondary API)
- **No treating a conditional permission as a grant** — see **RULE: Only an unconditional grant
  is true**

## CI Workflow Conventions

The repo uses one GitHub Actions workflow file per language plus `spicedb-gen.yaml` and `meta.yaml`. Rules for maintaining and extending CI:

1. **Per-language file.** Every language has its own `.github/workflows/<lang>.yaml`. Do not add language-specific jobs to a shared workflow. Cross-cutting checks (e.g., `mage gen:all` nodiff, YAML lint) live in `meta.yaml`.

2. **Standard job set per language workflow.** Each language workflow contains: `paths-filter`, `lint`, `unit`, `integration`, and `apicompat` (if the language appears in `apiCompatLanguages` in the root `Magefile.go`). All four work-jobs gate on `paths-filter.outputs.changed == 'true'`. `apicompat` additionally gates on `github.event_name == 'pull_request'` so it has a base ref to diff against.

3. **paths-filter scope.** Each filter must watch the workflow file itself, the language's `proto-clients/spicedb-<lang>-proto/**`, the language's `spicedb-<lang>/**`, root `Magefile.go`, and root `go.mod` / `go.sum`. Workflow edits without filter coverage produce a silently-skipping CI.

   **A filter must also watch what the job's code is compiled against, not only where that code lives.** `spicedb-gen.yaml` watched `spicedb-gen/**` alone, but the generator's templates emit code importing `spicedb-go/{client,consistency,rel}`, `spicedb-typescript/src`, `spicedb-python/spicedb`, and `spicedb-java/lib/src/main/java`, and every `testdata/` project builds against those live in-repo sources. A signature change in a consumed package could therefore break generation while every generator job skipped and reported green — which is exactly how a dropped-error regression reached review (finding F4). Scope such entries to the packages actually imported; a blanket `spicedb-*/**` runs the suite on every client change and invites someone to revert the whole filter as noise. Note the filter is necessary but not sufficient: pair it with a test that actually fails on the regression, since a discarded return value still compiles.

4. **Runner sizing.** Default to `depot-ubuntu-24.04-arm-4`. Use `arm-small` for trivial steps (paths-filter, apicompat, yaml lint). Use `arm-8` only for cross-cutting work that touches every language (e.g. `meta.gen-nodiff`). Justify any size deviation in a comment.

5. **Action pinning.** Third-party actions (anything outside `actions/*` and `authzed/*`) pin to commit SHA with `# vX.Y.Z` comment. Dependabot's `github-actions` ecosystem keeps these current.

6. **Integration tests.** Each `mage <lang> integrationTest` target is self-contained — it starts its own docker-compose, runs tests, tears down. CI runs at most one `integrationTest` per job (they all bind `localhost:50051`). Cross-language parallelism is fine because each runner is isolated.

7. **Adding a new language.** Checklist:
   - Create `.github/workflows/<lang>.yaml` from an existing one (Go is a good template if compiled, Python if interpreted).
   - Add the language's directories to `.github/dependabot.yml`.
   - Add a per-language Quick Start in `README.md` (typed if `spicedb-gen` supports it, idiomatic otherwise).
   - Add the language to `languages` (and `apiCompatLanguages` if applicable) in the root `Magefile.go`.

8. **Adding a new job type.** If you find yourself wanting a new kind of check (e.g., security scanning, license check), add it to **every** language workflow at once, document it here, and add a paths-filter clause if it should be skippable.
