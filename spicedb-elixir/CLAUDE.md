# spicedb-elixir

This is the idiomatic Elixir client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for Elixir-specific goals; this takes precedence
3. When updating after a proto change: check the proto client diff
4. NEVER remove or rename public API functions/structs; add new functions instead
5. Propagate deprecation with `@deprecated` and `IO.warn("[DEPRECATION] ...")` in the function body
6. Update `examples/` to cover new functionality
7. Never delete an example; mark deprecated ones with a note instead
8. Update CHANGELOG.md after making changes
9. Run `mise exec -- mix test` after making changes
10. Run `mise exec -- mix format --check-formatted`, `mise exec -- mix credo --strict` and `mise exec -- mix dialyzer` for linting
