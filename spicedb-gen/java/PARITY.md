# Generated Java capability inventory

The template adds 51 explicit native instance overloads. Relationship write/read,
bounded deletion, non-atomic deletion, import/export, bulk check/any/all,
schema read/write/reflection/diff/dependencies/computable permissions, expansion,
watch (including relationship filters), counters, materialize, and raw channel
access retain native parameter and return types. All forwarding returns native
values/streams directly without wrapping errors or materializing iterators.

The durable inventory is `TypedParityTest`: it discovers every public native
instance method, requires its exact parameter signature and generic result, and
fails on newly introduced omissions. It rejects native signatures for the three
explicit schema-aware exceptions: `check`, `lookupResources`, `lookupSubjects`.
Those capabilities remain directly available through their generated schema-aware
signatures; adding permissive native overloads would bypass subject constraints.
`close` remains present with deliberate owned-versus-borrowed lifecycle semantics.

Connection construction is not an instance capability. Existing generated
plaintext/system-TLS factories remain. Advanced native `create`/`ClientOption`
transport configuration uses `new TypedClient(nativeClient, ownsClient)` rather
than new permissive static factory overloads. Constants, nested result types,
transport configuration helpers, and Java Object methods are not duplicated.
There are no omitted native instance capability names.

Execution ledger: inventory test first failed with NoSuchMethodException on the
old fixture; explicit forwarding was then added. Runtime tests exercise deletion
cursor/options/progress, watch stream cancellation, one-pass chunked imports,
exports, bulk-check context/tracing, and experimental native results. Existing negative compilation fixtures
retain schema errors and reject treating WriteResult as a token.
