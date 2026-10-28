"""spicedb.protocols names each client's full surface, and the doubles implement it."""

from __future__ import annotations

import inspect

import pytest

from spicedb.aio import SpiceDBClient as AioClient
from spicedb.protocols import AsyncClientProtocol, ClientProtocol
from spicedb.sync import SpiceDBClient as SyncClient
from spicedb.testing import AsyncInMemoryClient, InMemoryClient

# The context-manager entry returns the implementing class itself, so its
# return annotation names the protocol rather than the client.
_ENTER = {"__enter__", "__aenter__"}
_CONTEXT = {"__enter__", "__exit__", "__aenter__", "__aexit__"}

FLAVORS = [
    pytest.param(ClientProtocol, SyncClient, InMemoryClient, id="sync"),
    pytest.param(AsyncClientProtocol, AioClient, AsyncInMemoryClient, id="aio"),
]


def _client_surface(cls) -> dict[str, object]:
    """Public instance methods plus the context-manager protocol.

    ``from_channel`` is a constructor (a classmethod), not part of an
    instance's surface, so it is excluded like Go's interface excludes
    ``New``.
    """
    return {
        name: member
        for name, member in inspect.getmembers(cls, inspect.isfunction)
        if not name.startswith("_") or name in _CONTEXT
    }


def _protocol_surface(protocol) -> dict[str, object]:
    return {
        name: member
        for name, member in vars(protocol).items()
        if inspect.isfunction(member) and (not name.startswith("_") or name in _CONTEXT)
    }


def _params(fn):
    return [(p.name, p.kind, p.default, p.annotation) for p in inspect.signature(fn).parameters.values()]


def _returns(fn) -> str:
    return str(inspect.signature(fn).return_annotation)


@pytest.mark.parametrize("protocol, client, double", FLAVORS)
def test_protocol_lists_exactly_the_clients_methods(protocol, client, double):
    assert set(_protocol_surface(protocol)) == set(_client_surface(client))
    assert "from_channel" not in _protocol_surface(protocol)


@pytest.mark.parametrize("protocol, client, double", FLAVORS)
def test_protocol_signatures_match_the_client(protocol, client, double):
    members = _protocol_surface(protocol)
    for name, real in _client_surface(client).items():
        declared = members[name]
        assert _params(declared) == _params(real), f"parameter drift in {name}()"
        assert inspect.iscoroutinefunction(declared) == inspect.iscoroutinefunction(real), name
        if name in _ENTER:
            assert _returns(declared) == protocol.__name__
        else:
            assert _returns(declared) == _returns(real), f"return drift in {name}()"


@pytest.mark.parametrize("protocol, client, double", FLAVORS)
def test_double_implements_the_whole_protocol(protocol, client, double):
    members = _protocol_surface(protocol)
    surface = _client_surface(double)
    for name, declared in members.items():
        assert name in surface, f"{double.__name__} lacks {name}()"
        implemented = surface[name]
        assert _params(implemented) == _params(declared), f"parameter drift in {double.__name__}.{name}()"
        assert inspect.iscoroutinefunction(implemented) == inspect.iscoroutinefunction(declared), name
        if name in _ENTER:
            assert _returns(implemented) == double.__name__
        else:
            assert _returns(implemented) == _returns(declared), f"return drift in {double.__name__}.{name}()"


def test_protocols_are_exported():
    import spicedb.protocols

    assert spicedb.protocols.__all__ == ["ClientProtocol", "AsyncClientProtocol"]
