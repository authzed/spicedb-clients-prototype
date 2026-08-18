"""Asynchronous SpiceDB client.

Use this flavor from async applications::

    from spicedb.aio import SpiceDBClient

    async with SpiceDBClient("localhost:50051", token="t", insecure=True) as c:
        result = await c.check_permission(full(), rel)
        if result.has_permission:  # True only for a full grant, never a Conditional
            ...

For a plaintext connection to anything other than a loopback endpoint, see
SpiceDBClient's docstring on `allow_insecure_remote_credentials` and root
DESIGN.md, "RULE: Credentials over insecure transport require an explicit
opt-in".

Synchronous callers want `spicedb.sync` instead.
"""

from spicedb.aio.client import SpiceDBClient

__all__ = ["SpiceDBClient"]
