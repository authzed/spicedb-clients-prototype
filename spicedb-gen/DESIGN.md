# spicedb-gen Design

`spicedb-gen` is a type-safe client code generator for SpiceDB. It reads a
`.zed` schema file and produces language-specific client code with strong typing
for object definitions, relations, and permissions.

## Architecture

```
.zed schema file
      |
      v
  schema/parse.go    -- parses schema using SpiceDB's DSL compiler
      |
      v
  schema/model.go    -- language-neutral intermediate representation
      |
      v
  generator/         -- pluggable language generators (LanguageGenerator interface)
      |
      v
  generated files    -- type-safe client code for the target language
```

## Key decisions

- **SpiceDB compiler as parser**: We use `pkg/schemadsl/compiler` directly
  rather than writing our own parser. This guarantees we parse exactly what
  SpiceDB accepts.
- **Reachable subjects**: For permissions, we walk the `UsersetRewrite` tree to
  compute which subject types are reachable. This enables generated code to
  constrain which subjects can be checked against each permission.
- **Generator registry**: Language generators register themselves via
  `generator.Register()`, keeping the core schema-parsing logic decoupled from
  any specific target language.
- **Generated code does not reinvent the idiomatic client's semantics**: a
  generated typed client is a compile-time-typed façade over the idiomatic
  client for that language. It must never widen, narrow, or reinterpret what
  the underlying client returns.

## The generated check surface

The typed `check` in every template returns the underlying idiomatic client's
`CheckResult` — not a bare `bool`. It inherits the root [`DESIGN.md`](../DESIGN.md)
contract wholesale: the three-valued `permissionship`, `missing_context`,
`checked_at`, and the RULE that only an unconditional grant is true. A generated
predicate that collapsed the result to a boolean would reintroduce, at the typed
layer, exactly the fail-open the idiomatic clients removed.

Generated `check` also accepts caveat context in the same call-level plus
per-item shape as the underlying client, with the same key-level merge (item
wins). The generator's own wrinkle: a *subject* can carry caveat context in the
generated API (`User("alice").WithTimeWindow(...)`), and that subject-embedded
context is the item-level half of the merge — it overrides the call-level
`context` argument per key, and call-level keys the subject does not mention are
retained.

Templates carrying this surface, one per supported language (Python has two,
one per flavor):

```
golang/templates/typed_client.go.tmpl
typescript/templates/typed_client.ts.tmpl
java/templates/typed_client.java.tmpl
python/templates/aio.py.tmpl
python/templates/sync.py.tmpl
```

Generated output is **executed** against a live SpiceDB, not merely compiled:
`mage integrationTest` runs the TypeScript, Go, Java, and Python testdata suites
in sequence.
