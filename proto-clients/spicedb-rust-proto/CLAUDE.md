# spicedb-rust-proto

This is a tonic-generated Rust proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the generated output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `proto/` — those come from `buf export`
4. Mark deprecated proto methods with `#[deprecated]` attributes
5. Run `cargo test` after making changes

## File layout

- `proto/` — buf-exported proto files (DO NOT MODIFY, not checked in)
- `build.rs` — tonic-build code generation from proto files
- `src/lib.rs` — module declarations and proto includes
- `src/client.rs` — SpiceDBProtoClient struct, constructor, interceptor
- `tests/client_test.rs` — integration tests

## Build prerequisites

Before `cargo build` will succeed, you must populate the proto directory:

```sh
buf export buf.build/authzed/api -o proto
```
