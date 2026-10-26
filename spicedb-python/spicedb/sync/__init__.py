"""Sync SpiceDB client and its owned stream type."""

from spicedb.sync.client import SpiceDBClient
from spicedb._streams import Stream

__all__ = ["SpiceDBClient", "Stream"]
