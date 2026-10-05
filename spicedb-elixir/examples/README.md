# spicedb-elixir Examples

Each subdirectory holds one ExUnit file, `<name>/<name>_test.exs`, that is both
documentation and an integration test.

## Running

`mage integrationTest` starts the SpiceDB container from
`docker-compose.test.yml`, runs every example against it, checks that each one
ran, and tears the container down.

To run them by hand against a SpiceDB of your own, from the package root:

```bash
docker compose -f docker-compose.test.yml up -d
SPICEDB_EXAMPLES=1 MIX_ENV=test mise exec -- mix test examples/

# one example
SPICEDB_EXAMPLES=1 MIX_ENV=test mise exec -- mix test examples/check_permission

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
  SPICEDB_EXAMPLES=1 MIX_ENV=test mise exec -- mix test examples/
```

`SPICEDB_EXAMPLES=1` is what points `mix test` at `examples/`; without it,
`mix test` runs the unit tests in `test/`. `SPICEDB_ENDPOINT` and
`SPICEDB_TOKEN` default to `localhost:50051` and `somerandomkeyhere`, the
endpoint and preshared key in `docker-compose.test.yml`.

Every example resets the server before each test. Point them only at a
disposable SpiceDB.

Tests tagged `:no_spicedb` skip the shared client setup and reset; most of them
bring up their own stand-in server.
