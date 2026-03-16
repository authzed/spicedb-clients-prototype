# spicedb-python-proto

This is a buf-generated Python proto client for SpiceDB.

## What to do

1. Read DESIGN.md — it specifies exactly what code to add beyond the buf output
2. Only add code specified in the DESIGN.md manifest — nothing extra
3. Don't touch files under `gen/` — those are produced by buf generate
4. Mark deprecated proto methods with `warnings.warn(..., DeprecationWarning)`
5. Run `uv run pytest` after making changes
