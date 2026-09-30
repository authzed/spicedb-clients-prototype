# Changelog

## Unreleased

### Added

- Initial idiomatic Elixir client, `spicedb`, over the `spicedb_proto` tier.
  Covers checks (single, bulk, `check_any`, `check_all`), relationship
  write/read/delete, lookups, expand, bulk import and export, watch, schema
  read/write/reflection/diff, relationship counters, and the materialize-tier
  experimental calls. Every call returns `{:ok, value}` or `{:error, exception}`
  and has a raising `!` variant; `proto_client/1` is the escape hatch to the
  generated stubs.
