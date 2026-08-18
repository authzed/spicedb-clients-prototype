"""Synchronous SpiceDB client.

Use this flavor from synchronous applications -- Django, Flask, scripts, batch
jobs. It needs no event loop::

    from spicedb.sync import SpiceDBClient

    client = SpiceDBClient("localhost:50051", token="t", insecure=True)
    result = client.check_permission(full(), rel)
    if result.has_permission:  # True only for a full grant, never a Conditional
        ...

Async callers want `spicedb.aio` instead.
"""

from spicedb.sync.client import SpiceDBClient

__all__ = ["SpiceDBClient"]
