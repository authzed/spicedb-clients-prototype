"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""

from spicedb import SpiceDBClient


def test_constructor_insecure():
    c = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
    assert c._channel is not None


async def test_context_manager():
    async with SpiceDBClient(
        "localhost:50051", token="testtoken", insecure=True
    ) as c:
        assert c._channel is not None
