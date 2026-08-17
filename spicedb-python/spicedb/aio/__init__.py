"""Asynchronous SpiceDB client.

Use this flavor from async applications::

    from spicedb.aio import SpiceDBClient

    async with SpiceDBClient("localhost:50051", token="t", insecure=True) as c:
        result = await c.check_permission(full(), rel)
        if result.has_permission:  # True only for a full grant, never a Conditional
            ...

Synchronous callers want `spicedb.sync` instead.
"""

from spicedb.aio.client import SpiceDBClient

__all__ = ["SpiceDBClient"]
