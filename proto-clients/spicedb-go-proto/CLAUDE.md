# spicedb-go-proto

This is a buf-generated Go proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `// Deprecated:` comments
5. Run `go test ./...` after making changes

## File layout

- `gen/` — buf-generated code (DO NOT MODIFY)
- `client.go` — Client struct, constructor, options
- `types.go` — re-exported proto types
- `client_test.go` — tests
