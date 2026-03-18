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
