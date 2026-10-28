"""Named choices for client behavior."""
from __future__ import annotations

import math
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from enum import Enum
from typing import Any

import grpc

from spicedb.errors import InvalidArgumentError, SpiceDBError

class Tracing(Enum):
    DISABLED = False
    ENABLED = True

class LookupPermissionshipFilter(Enum):
    ALL = "all"
    HAS_PERMISSION_ONLY = "has_permission_only"

class LookupDebug(Enum):
    DISABLED = False
    ENABLED = True

class Wildcards(Enum):
    DEFAULT = 0
    INCLUDE = 1
    EXCLUDE = 2

class WatchContent(Enum):
    """Watch content kinds; combine SCHEMA and CHECKPOINTS with RELATIONSHIPS freely."""
    RELATIONSHIPS = 1
    SCHEMA = 2
    CHECKPOINTS = 3

class TransportSecurity(Enum):
    TLS = "tls"
    PLAINTEXT = "plaintext"

class RemoteCredentials(Enum):
    REJECT = "reject"
    ALLOW = "allow"


class DefaultDeadline(Enum):
    """Pass ``default_timeout=DefaultDeadline.DISABLED`` to remove the 30s default.

    Unary operations then run unbounded unless the call passes ``timeout=``.
    """

    DISABLED = "disabled"


class Retries(Enum):
    """Pass ``retry_policy=Retries.DISABLED`` to turn off every automatic retry."""

    DISABLED = "disabled"


class StreamRetries(Enum):
    """Whether a streaming read is re-established after a retryable failure.

    Retries only ever happen before the stream exposes its first item. Watch
    streams never retry. DISABLED leaves unary retry unchanged.
    """

    ENABLED = "enabled"
    DISABLED = "disabled"


class BulkItemErrors(Enum):
    """How ``check_bulk`` reports a per-item error.

    RAISE (default) raises the first item's error. RETURN puts each failing
    item's error on ``CheckResult.error`` (never a grant) and returns every
    other result.
    """

    RAISE = "raise"
    RETURN = "return"


class DeletePartial(Enum):
    """Whether ``delete_relationships`` may delete part of the matches.

    ALLOWED (default) deletes up to ``limit`` and reports PARTIAL progress.
    DISALLOWED is all-or-nothing: the server rejects a delete that exceeds
    its limit; ``limit`` may then be omitted to use the server default.
    """

    ALLOWED = "allowed"
    DISALLOWED = "disallowed"


@dataclass(frozen=True)
class RetryAttempt:
    """What ``RetryPolicy.on_retry`` receives before each retry."""

    method: str
    """The gRPC method name, e.g. ``"ReadSchema"``."""
    attempt: int
    """1 for the first retry, 2 for the second, and so on."""
    error: SpiceDBError
    """The failure being retried."""
    delay: float
    """Seconds the client will wait before retrying."""


_DEFAULT_RETRYABLE = frozenset({grpc.StatusCode.UNAVAILABLE, grpc.StatusCode.ABORTED})


@dataclass(frozen=True)
class RetryPolicy:
    """Client-wide retry policy for idempotent reads.

    The default retries UNAVAILABLE and ABORTED up to three times with
    full-jitter exponential backoff starting at 100 ms and capped at 5 s. Any
    read code may be added, including RESOURCE_EXHAUSTED. Mutations (writes,
    deletes, imports, schema writes, counter registration) are never retried
    under any policy. Every retry shares the operation's deadline.
    """

    max_retries: int = 3
    retryable_codes: frozenset[grpc.StatusCode] = _DEFAULT_RETRYABLE
    initial_backoff: float = 0.1
    max_backoff: float = 5.0
    on_retry: Callable[[RetryAttempt], Any] | None = None

    def __post_init__(self) -> None:
        if isinstance(self.max_retries, bool) or not isinstance(self.max_retries, int) or self.max_retries < 0:
            raise InvalidArgumentError("retry_policy.max_retries: expected non-negative integer")
        codes = frozenset(self.retryable_codes)
        if not all(isinstance(code, grpc.StatusCode) for code in codes):
            raise InvalidArgumentError("retry_policy.retryable_codes: expected grpc.StatusCode values")
        if grpc.StatusCode.OK in codes:
            raise InvalidArgumentError("retry_policy.retryable_codes: OK is not an error")
        object.__setattr__(self, "retryable_codes", codes)
        for name in ("initial_backoff", "max_backoff"):
            value = getattr(self, name)
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
                raise InvalidArgumentError(f"retry_policy.{name}: expected finite non-negative number")
        if self.on_retry is not None and not callable(self.on_retry):
            raise InvalidArgumentError("retry_policy.on_retry: expected callable")


DEFAULT_RETRY_POLICY = RetryPolicy()
_NO_RETRIES = RetryPolicy(max_retries=0)


def _retry_policy(value: RetryPolicy | Retries) -> RetryPolicy:
    if value is Retries.DISABLED:
        return _NO_RETRIES
    if not isinstance(value, RetryPolicy):
        raise InvalidArgumentError(f"retry_policy: expected RetryPolicy or Retries.DISABLED, got {value!r}")
    return value


@dataclass
class ResponseMetadata:
    """Mutable capture of one operation's response headers and trailers.

    Pass it in ``CallOptions(response_metadata=...)``. After the operation
    returns (or raises), ``headers`` and ``trailers`` hold what the server
    sent on the last RPC attempt; for a stream, after the stream is consumed
    or closed. They stay empty when nothing was received.
    """

    headers: tuple[tuple[str, str | bytes], ...] = ()
    trailers: tuple[tuple[str, str | bytes], ...] = ()


@dataclass(frozen=True)
class CallOptions:
    """Per-call transport escape hatch, accepted by every operation.

    The supported route to transport-level data (for example a server
    version header, or trailers older SpiceDB versions send) without the raw
    gRPC client. ``metadata`` is sent in addition to the client's own
    authorization header; an ``authorization`` entry is rejected on a client
    that has a token (it may supply one on a ``from_channel`` client). ``wait_for_ready`` and
    ``compression`` are forwarded to grpc. Applies to every attempt, chunk
    and page of the operation.
    """

    metadata: Sequence[tuple[str, str | bytes]] = ()
    wait_for_ready: bool | None = None
    compression: grpc.Compression | None = None
    response_metadata: ResponseMetadata | None = field(default=None, compare=False)

    def __post_init__(self) -> None:
        pairs = tuple(tuple(pair) for pair in self.metadata)
        for pair in pairs:
            if len(pair) != 2 or not isinstance(pair[0], str):
                raise InvalidArgumentError("call_options.metadata: expected (str, value) pairs")
        object.__setattr__(self, "metadata", pairs)
        if self.wait_for_ready is not None and not isinstance(self.wait_for_ready, bool):
            raise InvalidArgumentError("call_options.wait_for_ready: expected bool")
        if self.compression is not None and not isinstance(self.compression, grpc.Compression):
            raise InvalidArgumentError("call_options.compression: expected grpc.Compression")
        if self.response_metadata is not None and not isinstance(self.response_metadata, ResponseMetadata):
            raise InvalidArgumentError("call_options.response_metadata: expected ResponseMetadata")


def _option(value, kind):
    if not isinstance(value, kind):
        raise InvalidArgumentError(f"expected {kind.__name__}, got {value!r}")
    return value.value
