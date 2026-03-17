# SpiceDB Client Libraries — Idiomatic Design Vision

## Philosophy

SpiceDB clients should feel native to each language. Users should not need to
know about protobuf or gRPC to use them. The client API should guide users
toward correct, performant usage patterns ("pit of success").

## Backwards Compatibility Mandate

**Public API surfaces are append-only.**

- Never remove methods or types from the public API
- Never change method signatures
- Deprecate with new alternatives — add new methods instead of changing existing ones
- Examples must always compile and pass
- This applies even when idiomatic improvements suggest changes

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
   mechanism for the language (Go: iter.Seq2, Python: async iterators,
   TypeScript: AsyncIterableIterator)
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

## What NOT To Do

- No auto-generated feeling — the API should read like hand-written code
- No protobuf types in the public API — wrap them in language-native types
- No requiring users to construct nested proto messages
- No silent defaults for consistency — make users choose explicitly
- No exposing gRPC internals (channels, stubs, metadata) in the primary API
  (escape hatches for advanced use are acceptable as clearly marked secondary API)
