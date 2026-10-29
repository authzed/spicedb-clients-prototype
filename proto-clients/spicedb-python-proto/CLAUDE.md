# spicedb-python-proto

This is a buf-generated Python proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate. Never
   add an `__init__.py` there: the shared packages must stay namespace packages
   so a bridged authzed-py can coexist (DESIGN.md, "Canonical binding and the
   authzed-py bridge"). If a regeneration adds or removes a `*_pb2.py`, update
   `CANONICAL_MODULES` in `spicedb_python_proto/bridge.py`.
4. Mark deprecated proto methods with `warnings.warn(..., DeprecationWarning)`
5. Run `uv run pytest` after making changes. `tests/test_bridge.py` needs `uv`
   and network access (PyPI) or a warm uv cache, and fails rather than skips
   without them. Set `SPICEDB_ENDPOINT` to include the live coexistence check.
