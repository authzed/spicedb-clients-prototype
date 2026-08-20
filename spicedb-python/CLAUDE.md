# spicedb-python

This is the idiomatic Python client for SpiceDB.

## What to do

1. Read `../DESIGN.md` (root) for overall vision and backwards compat rules
2. Read `./DESIGN.md` for Python-specific goals — this takes precedence
3. When updating after a proto change: check the proto client diff
4. NEVER remove or rename public API methods/types — add new methods instead
5. Propagate deprecation using `warnings.warn(..., DeprecationWarning)`
6. `spicedb.sync.SpiceDBClient` and `spicedb.aio.SpiceDBClient` must change
   together — same method names, same signatures, differing only in
   `async`/`await` and `Iterator` vs `AsyncIterator`. `tests/test_parity.py`
   enforces this and fails the build on any drift.
7. Update `examples/` to cover new functionality
8. Never delete an example — mark deprecated ones with a note instead
9. Update CHANGELOG.md after making changes
10. Run `uv run pytest` after making changes

## Test prerequisites

`openssl` must be on `PATH`. The custom-TLS fixtures — `tests/test_custom_tls.py`,
`tests/test_auth_headers.py` and `examples/custom_tls/` — shell out to it to
generate a throwaway CA and sign the certificates their real TLS servers present.
They **fail** rather than skip when it is missing (a skipped test reads as
coverage while proving nothing), so on a minimal image you get
`FileNotFoundError: 'openssl'` with no hint. Every other test here runs without
it. CI is fine: the workflows run on `depot-ubuntu-24.04-arm-*`, which ships
openssl.
