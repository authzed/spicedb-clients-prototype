"""Consistency strategy constructors for SpiceDB operations."""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from spicedb.options import _option
from spicedb.errors import InvalidArgumentError

from authzed.api.v1 import permission_service_pb2
from authzed.api.v1 import core_pb2
from spicedb.token import ZedToken, _token_string


class ConsistencyMode(Enum):
    FULL = "fully_consistent"
    MIN_LATENCY = "minimize_latency"
    AT_LEAST = "at_least_as_fresh"
    SNAPSHOT = "at_exact_snapshot"


@dataclass(frozen=True)
class Consistency:
    """Opaque consistency strategy.

    Construct via ``full()``/``min_latency()``/``at_least()``/``snapshot()``/
    ``at_least_or_full()``/``at_least_or_min_latency()`` — never directly.
    """

    strategy: ConsistencyMode
    token: ZedToken | None = None

    def __post_init__(self) -> None:
        _option(self.strategy, ConsistencyMode)
        if self.strategy in (ConsistencyMode.AT_LEAST, ConsistencyMode.SNAPSHOT):
            if self.token is None or not _token_string(self.token):
                raise InvalidArgumentError("consistency: expected nonempty ZedToken")
        elif self.token is not None:
            raise InvalidArgumentError("consistency: this mode does not accept a token")

    def _to_proto(self) -> permission_service_pb2.Consistency:
        if self.strategy in (ConsistencyMode.FULL, ConsistencyMode.MIN_LATENCY):
            return permission_service_pb2.Consistency(**{self.strategy.value: True})
        assert self.token is not None
        return permission_service_pb2.Consistency(
            **{self.strategy.value: core_pb2.ZedToken(token=_token_string(self.token))}
        )


def full() -> Consistency:
    """Require fully consistent results."""
    return Consistency(ConsistencyMode.FULL)


def min_latency() -> Consistency:
    """Minimize latency, allowing slightly stale results."""
    return Consistency(ConsistencyMode.MIN_LATENCY)


def at_least(revision: ZedToken) -> Consistency:
    """Require results at least as fresh as the given revision."""
    _token_string(revision)
    return Consistency(ConsistencyMode.AT_LEAST, revision)


def at_least_or_full(revision: ZedToken | None) -> Consistency:
    """Return at_least(revision) if revision is non-empty, otherwise full().

    Use this when you have an optional revision from a previous write and
    want the safest fallback.
    """
    if revision is not None and _token_string(revision):
        return at_least(revision)
    return full()


def at_least_or_min_latency(revision: ZedToken | None) -> Consistency:
    """Return at_least(revision) if revision is non-empty, otherwise min_latency().

    Use this when you have an optional revision but prefer performance over
    full consistency as the fallback.
    """
    if revision is not None and _token_string(revision):
        return at_least(revision)
    return min_latency()


def snapshot(revision: ZedToken) -> Consistency:
    """Require results from an exact snapshot revision."""
    _token_string(revision)
    return Consistency(ConsistencyMode.SNAPSHOT, revision)
