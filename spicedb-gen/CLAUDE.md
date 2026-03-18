# spicedb-gen

## What this is

A code generator that reads SpiceDB `.zed` schemas and produces type-safe
client code. The `schema/` package parses schemas; the `generator/` package
defines the pluggable generator interface.

## Rules

- Do not modify `schema/model.go` without considering all existing generators.
- The schema parser uses SpiceDB's compiler directly — do not reimplement
  parsing logic.
- All generators must implement `generator.LanguageGenerator` and register via
  `generator.Register()`.
- Run `go test ./schema/ -v` after any changes to the parser.
