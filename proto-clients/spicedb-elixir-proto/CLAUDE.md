# spicedb-elixir-proto

This is a buf-generated Elixir proto client for SpiceDB.

## What to do

1. Read DESIGN.md -- it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest -- nothing extra
3. Don't touch files under `lib/gen/` -- those are produced by buf generate
4. Mark deprecated proto methods with `@deprecated "..."` on the generated module and `IO.warn("[DEPRECATION] ...")` at the call site
5. Run `mix format --check-formatted && mix compile --warnings-as-errors && mix test` after making changes

## File layout

- `lib/gen/` -- buf-generated code (DO NOT MODIFY)
- `lib/spicedb_proto/client.ex` -- Client struct, connect/3, loopback guard
- `test/spicedb_proto/client_test.exs` -- tests
