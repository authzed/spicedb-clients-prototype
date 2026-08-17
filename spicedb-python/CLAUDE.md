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
