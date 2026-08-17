"""The sync and async clients must expose the same surface.

Spec D2 chose hand-written flavors over codegen. This test is the mitigation:
it fails the build the moment the two surfaces disagree.
"""

from __future__ import annotations

import inspect
import typing

import pytest

from spicedb.aio import SpiceDBClient as AsyncClient
from spicedb.sync import SpiceDBClient as SyncClient

# close/__enter__/__aenter__ intentionally differ in shape between flavors.
_LIFECYCLE = {"close"}


def _public_methods(cls) -> dict[str, typing.Any]:
    return {
        name: member
        for name, member in inspect.getmembers(cls, inspect.isfunction)
        if not name.startswith("_")
    }


def test_same_public_method_names():
    assert set(_public_methods(SyncClient)) == set(_public_methods(AsyncClient))


@pytest.mark.parametrize("name", sorted(set(_public_methods(AsyncClient)) - _LIFECYCLE))
def test_signatures_match(name: str):
    """Parameter names, kinds, and defaults must be identical."""
    sync_params = list(inspect.signature(_public_methods(SyncClient)[name]).parameters.values())
    aio_params = list(inspect.signature(_public_methods(AsyncClient)[name]).parameters.values())

    assert [(p.name, p.kind, p.default) for p in sync_params] == [
        (p.name, p.kind, p.default) for p in aio_params
    ], f"signature drift in {name}()"


@pytest.mark.parametrize("name", sorted(set(_public_methods(AsyncClient)) - _LIFECYCLE))
def test_return_annotations_match_after_normalization(name: str):
    """AsyncIterator[T] on aio must correspond to Iterator[T] on sync."""
    sync_ret = str(inspect.signature(_public_methods(SyncClient)[name]).return_annotation)
    aio_ret = str(inspect.signature(_public_methods(AsyncClient)[name]).return_annotation)
    normalized = aio_ret.replace("AsyncIterator", "Iterator")
    assert sync_ret == normalized, f"return-type drift in {name}()"


def test_no_sync_method_is_a_coroutine_or_async_generator():
    for name, fn in _public_methods(SyncClient).items():
        assert not inspect.iscoroutinefunction(fn), f"{name} must not be async"
        assert not inspect.isasyncgenfunction(fn), f"{name} must not be an async generator"


def test_every_aio_method_is_awaitable_or_an_async_generator():
    for name, fn in _public_methods(AsyncClient).items():
        assert inspect.iscoroutinefunction(fn) or inspect.isasyncgenfunction(fn), (
            f"{name} on the aio client is neither a coroutine nor an async generator"
        )


def test_docstrings_present_on_both():
    for name, fn in _public_methods(SyncClient).items():
        assert fn.__doc__, f"sync {name}() is missing its docstring"
    for name, fn in _public_methods(AsyncClient).items():
        assert fn.__doc__, f"aio {name}() is missing its docstring"


def test_constructor_signatures_match():
    sync_sig = inspect.signature(SyncClient.__init__)
    aio_sig = inspect.signature(AsyncClient.__init__)
    assert [(p.name, p.kind, p.default) for p in sync_sig.parameters.values()] == [
        (p.name, p.kind, p.default) for p in aio_sig.parameters.values()
    ]
