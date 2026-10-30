"""Named choices for client behavior."""
from __future__ import annotations

import math
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from enum import Enum
from typing import Any

import grpc

from spicedb.errors import LocalValidationError, SpiceDBError

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
    streams never retry unless one call opts in with
    ``CallOptions(retry_policy=...)``, or the client sets a policy for the
    operation with ``operation_retry_policies``; either governs stream
    establishment regardless of this setting. DISABLED leaves unary retry
    unchanged.
    """

    ENABLED = "enabled"
    DISABLED = "disabled"


class Paging(Enum):
    """How ``read_relationships`` and ``lookup_resources`` fetch results.

    AUTOMATIC (default) fetches page after page of ``page_size`` until the
    results (or ``limit``) run out. SINGLE_REQUEST sends exactly one request,
    as the raw API does: its limit is ``limit`` (unbounded without one),
    ``page_size`` is ignored, and no follow-up page is requested. Each item
    still carries its ``after_result_cursor``.
    """

    AUTOMATIC = "automatic"
    SINGLE_REQUEST = "single_request"


class BulkItemErrors(Enum):
    """How ``check_bulk`` reports a per-item error.

    RAISE (default) raises the first item's error. RETURN puts each failing
    item's error on ``CheckResult.error`` (never a grant) and returns every
    other result. RETURN covers per-item errors the server reports; a
    malformed item in the server's response (such as an unset
    permissionship) is not one, and still fails the whole call with the
    client-detected malformed-response ``InternalError``.
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
    """Retry policy for idempotent reads, client-wide or for one call.

    The default retries UNAVAILABLE and ABORTED up to three times with
    full-jitter exponential backoff starting at 100 ms and capped at 5 s. Any
    read code may be added, including RESOURCE_EXHAUSTED. Mutations are never
    retried automatically; only a write whose transaction holds only touches
    and no preconditions (``Transaction.is_idempotent()``), and a schema
    write, may opt in, for one call through ``CallOptions(retry_policy=...)``
    or for every call of one operation through the client's
    ``operation_retry_policies``. Every retry shares the operation's deadline.

    Precedence, most specific first: ``CallOptions(retry_policy=...)``, then
    ``operation_retry_policies={Operation.X: ...}`` given to the client
    constructor, then the client-wide ``retry_policy``. A per-call or
    per-operation policy also governs stream establishment (watch included).
    A per-operation policy for ``Operation.WRITE`` retries only idempotent
    writes and leaves the others un-retried, without error; operations that
    are never idempotent (deletes, delete-all, imports, counter registration
    and unregistration) reject a per-operation policy with retries when the
    client is built. A zero-retry policy (or ``Retries.DISABLED``) is allowed
    for any operation.
    """

    max_retries: int = 3
    retryable_codes: frozenset[grpc.StatusCode] = _DEFAULT_RETRYABLE
    initial_backoff: float = 0.1
    max_backoff: float = 5.0
    on_retry: Callable[[RetryAttempt], Any] | None = None

    def __post_init__(self) -> None:
        if isinstance(self.max_retries, bool) or not isinstance(self.max_retries, int) or self.max_retries < 0:
            raise LocalValidationError("retry_policy.max_retries: expected non-negative integer")
        codes = frozenset(self.retryable_codes)
        if not all(isinstance(code, grpc.StatusCode) for code in codes):
            raise LocalValidationError("retry_policy.retryable_codes: expected grpc.StatusCode values")
        if grpc.StatusCode.OK in codes:
            raise LocalValidationError("retry_policy.retryable_codes: OK is not an error")
        object.__setattr__(self, "retryable_codes", codes)
        for name in ("initial_backoff", "max_backoff"):
            value = getattr(self, name)
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0:
                raise LocalValidationError(f"retry_policy.{name}: expected finite non-negative number")
        if self.on_retry is not None and not callable(self.on_retry):
            raise LocalValidationError("retry_policy.on_retry: expected callable")


DEFAULT_RETRY_POLICY = RetryPolicy()
_NO_RETRIES = RetryPolicy(max_retries=0)


def _retry_policy(value: RetryPolicy | Retries, field: str = "retry_policy") -> RetryPolicy:
    if value is Retries.DISABLED:
        return _NO_RETRIES
    if not isinstance(value, RetryPolicy):
        raise LocalValidationError(f"{field}: expected RetryPolicy or Retries.DISABLED, got {value!r}")
    return value


class Operation(Enum):
    """One client operation, valued by its method name.

    Keys ``operation_retry_policies`` on the client constructors and names
    the calls the :mod:`spicedb.testing` doubles record and fail.
    """

    CHECK = "check"
    CHECK_BULK = "check_bulk"
    WRITE = "write"
    DELETE_RELATIONSHIPS = "delete_relationships"
    DELETE_ALL_RELATIONSHIPS_NON_ATOMIC = "delete_all_relationships_non_atomic"
    READ_RELATIONSHIPS = "read_relationships"
    LOOKUP_RESOURCES = "lookup_resources"
    LOOKUP_SUBJECTS = "lookup_subjects"
    EXPORT_RELATIONSHIPS = "export_relationships"
    IMPORT_RELATIONSHIPS = "import_relationships"
    WATCH = "watch"
    READ_SCHEMA = "read_schema"
    WRITE_SCHEMA = "write_schema"
    REFLECT_SCHEMA = "reflect_schema"
    DIFF_SCHEMA = "diff_schema"
    COMPUTABLE_PERMISSIONS = "computable_permissions"
    DEPENDENT_RELATIONS = "dependent_relations"
    EXPAND_PERMISSION_TREE = "expand_permission_tree"
    EXPERIMENTAL_REGISTER_RELATIONSHIP_COUNTER = "experimental_register_relationship_counter"
    EXPERIMENTAL_UNREGISTER_RELATIONSHIP_COUNTER = "experimental_unregister_relationship_counter"
    EXPERIMENTAL_COUNT_RELATIONSHIPS = "experimental_count_relationships"
    EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER = "experimental_count_relationships_by_filter"
    EXPERIMENTAL_ROARING_LOOKUP_RESOURCES = "experimental_roaring_lookup_resources"
    WATCH_PERMISSION_SETS = "watch_permission_sets"
    LOOKUP_PERMISSION_SETS = "lookup_permission_sets"
    DOWNLOAD_PERMISSION_SETS = "download_permission_sets"


# Mutations that are never idempotent on replay: a client-wide retrying
# policy for one of them is rejected when the client is built.
_NEVER_IDEMPOTENT = frozenset({
    Operation.DELETE_RELATIONSHIPS,
    Operation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC,
    Operation.IMPORT_RELATIONSHIPS,
    Operation.EXPERIMENTAL_REGISTER_RELATIONSHIP_COUNTER,
    Operation.EXPERIMENTAL_UNREGISTER_RELATIONSHIP_COUNTER,
})


def _operation_retry_policies(value) -> dict[Operation, RetryPolicy]:
    """Validate ``operation_retry_policies`` into a private snapshot."""
    if value is None:
        return {}
    try:
        items = list(value.items())
    except AttributeError:
        raise LocalValidationError(
            "operation_retry_policies: expected a mapping of Operation to RetryPolicy or Retries.DISABLED"
        ) from None
    policies: dict[Operation, RetryPolicy] = {}
    for operation, policy in items:
        if not isinstance(operation, Operation):
            raise LocalValidationError(f"operation_retry_policies: expected Operation keys, got {operation!r}")
        resolved = _retry_policy(policy, f"operation_retry_policies[{operation.name}]")
        if operation in _NEVER_IDEMPOTENT and resolved.max_retries > 0:
            raise LocalValidationError(
                f"operation_retry_policies[{operation.name}]: {operation.value} cannot be retried: "
                "it is not idempotent on replay"
            )
        policies[operation] = resolved
    return policies


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

    ``retry_policy`` overrides the client's retry policy for this one call
    (and its ``operation_retry_policies`` entry for the operation),
    including stream establishment (before the first item): pass
    ``Retries.DISABLED`` to exempt one stream, such as a supervised watch,
    while the client retries everything else, or a ``RetryPolicy`` to retry
    a stream the client would not. Mutations are not retried by default;
    only ``write`` of a transaction holding only touches and no
    preconditions, and ``write_schema``, are idempotent on replay and may opt
    in. Every other mutation given a policy with retries raises
    ``InvalidArgumentError`` before any request.
    """

    metadata: Sequence[tuple[str, str | bytes]] = ()
    wait_for_ready: bool | None = None
    compression: grpc.Compression | None = None
    response_metadata: ResponseMetadata | None = field(default=None, compare=False)
    retry_policy: RetryPolicy | Retries | None = None

    def __post_init__(self) -> None:
        pairs = tuple(tuple(pair) for pair in self.metadata)
        for pair in pairs:
            if len(pair) != 2 or not isinstance(pair[0], str):
                raise LocalValidationError("call_options.metadata: expected (str, value) pairs")
        object.__setattr__(self, "metadata", pairs)
        if self.wait_for_ready is not None and not isinstance(self.wait_for_ready, bool):
            raise LocalValidationError("call_options.wait_for_ready: expected bool")
        if self.compression is not None and not isinstance(self.compression, grpc.Compression):
            raise LocalValidationError("call_options.compression: expected grpc.Compression")
        if self.response_metadata is not None and not isinstance(self.response_metadata, ResponseMetadata):
            raise LocalValidationError("call_options.response_metadata: expected ResponseMetadata")
        if self.retry_policy is not None:
            _retry_policy(self.retry_policy, "call_options.retry_policy")


def _option(value, kind):
    if not isinstance(value, kind):
        raise LocalValidationError(f"expected {kind.__name__}, got {value!r}")
    return value.value
