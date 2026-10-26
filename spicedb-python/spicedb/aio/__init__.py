"""Aio SpiceDB client and its owned stream type."""

from spicedb.aio.client import SpiceDBClient
from spicedb._streams import AsyncStream

__all__ = ["SpiceDBClient", "AsyncStream"]
