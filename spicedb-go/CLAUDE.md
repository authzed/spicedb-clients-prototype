# spicedb-go

This is the idiomatic Go client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for Go-specific goals — this takes precedence
3. When updating after a proto change: check the proto client diff to understand
   what changed
4. NEVER remove or rename public API methods/types — add new methods instead
5. Propagate deprecation markers using `// Deprecated: use XYZ instead.`
6. Update `examples/` to cover new functionality
7. Never delete an example — mark deprecated ones with a note instead
8. Update CHANGELOG.md after making changes
9. Run `go test ./...` and verify all examples compile with `go build ./examples/...`
