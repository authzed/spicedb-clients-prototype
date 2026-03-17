# spicedb-ruby-proto

This is a buf-generated Ruby proto client for SpiceDB.

## What to do

1. Read DESIGN.md -- it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest -- nothing extra
3. Don't touch files under `lib/gen/` -- those are produced by buf generate
4. Mark deprecated proto methods with `warn "[DEPRECATION] ..."`
5. Run `bundle exec rspec` after making changes

## File layout

- `lib/gen/` -- buf-generated code (DO NOT MODIFY)
- `lib/spicedb_proto.rb` -- top-level require file
- `lib/spicedb_proto/client.rb` -- Client class, interceptor
- `spec/client_spec.rb` -- tests
