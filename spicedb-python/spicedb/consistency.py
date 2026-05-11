"""Consistency strategy constructors for SpiceDB operations."""

from __future__ import annotations

from typing import TypeAlias

from authzed.api.v1 import permission_service_pb2
from authzed.api.v1.core_pb2 import ZedToken

# TypeAlias annotation makes Consistency usable as a type annotation in
# downstream code that runs pyright in strict / standard mode. Without it,
# the bare ``Consistency = permission_service_pb2.Consistency`` assignment
# is treated as a variable, and pyright rejects ``c: Consistency`` annotations
# with reportInvalidTypeForm.
Consistency: TypeAlias = permission_service_pb2.Consistency


def full() -> Consistency:
    """Require fully consistent results."""
    return Consistency(fully_consistent=True)


def min_latency() -> Consistency:
    """Minimize latency, allowing slightly stale results."""
    return Consistency(minimize_latency=True)


def at_least(revision: str) -> Consistency:
    """Require results at least as fresh as the given revision."""
    return Consistency(at_least_as_fresh=ZedToken(token=revision))


def at_least_or_full(revision: str) -> Consistency:
    """Return at_least(revision) if revision is non-empty, otherwise full().

    Use this when you have an optional revision from a previous write and
    want the safest fallback.
    """
    if revision:
        return at_least(revision)
    return full()


def at_least_or_min_latency(revision: str) -> Consistency:
    """Return at_least(revision) if revision is non-empty, otherwise min_latency().

    Use this when you have an optional revision but prefer performance over
    full consistency as the fallback.
    """
    if revision:
        return at_least(revision)
    return min_latency()


def snapshot(revision: str) -> Consistency:
    """Require results from an exact snapshot revision."""
    return Consistency(at_exact_snapshot=ZedToken(token=revision))
