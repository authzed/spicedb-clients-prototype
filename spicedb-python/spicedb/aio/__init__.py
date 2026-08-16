"""Asynchronous SpiceDB client.

Use this flavor from async applications::

    from spicedb.aio import SpiceDBClient

    async with SpiceDBClient("localhost:50051", token="t", insecure=True) as c:
        allowed = await c.check_permission(full(), rel)

Synchronous callers want `spicedb.sync` instead.
"""

from spicedb.aio.client import SpiceDBClient

__all__ = ["SpiceDBClient"]
