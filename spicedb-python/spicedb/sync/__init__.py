"""Synchronous SpiceDB client.

Use this flavor from synchronous applications -- Django, Flask, scripts, batch
jobs. It needs no event loop::

    from spicedb.sync import SpiceDBClient

    client = SpiceDBClient("localhost:50051", token="t", insecure=True)
    allowed = client.check_permission(full(), rel)

Async callers want `spicedb.aio` instead.
"""

from spicedb.sync.client import SpiceDBClient

__all__ = ["SpiceDBClient"]
