"""Configuration and call plumbing shared by the sync and async clients.

MUST NOT perform I/O: both flavors call these helpers and own their own
channel, sleeping and awaiting.
"""

from __future__ import annotations

import inspect
import random
from typing import Any

import grpc

from spicedb._budget import Budget
from spicedb.errors import InvalidArgumentError, SpiceDBError
from spicedb.options import (
    CallOptions,
    DefaultDeadline,
    ResponseMetadata,
    RetryAttempt,
    RetryPolicy,
    Retries,
    StreamRetries,
    _NO_RETRIES,
    _retry_policy,
)


def _positive(value, field):
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise InvalidArgumentError(f"{field}: expected positive integer")
    return value


def _method_name(rpc) -> str:
    method = getattr(rpc, "_method", b"")
    if isinstance(method, bytes):
        method = method.decode("ascii", "replace")
    return str(method).rsplit("/", 1)[-1] if method else ""


def _pairs(metadata) -> tuple[tuple[str, Any], ...]:
    try:
        return tuple((key, value) for key, value in (metadata or ()))
    except Exception:  # noqa: BLE001 -- best-effort capture
        return ()


def capture_sync(target: ResponseMetadata | None, call) -> None:
    """Copy headers/trailers from a sync call or RpcError into ``target``."""
    if target is None or call is None:
        return
    for attr, name in (("initial_metadata", "headers"), ("trailing_metadata", "trailers")):
        getter = getattr(call, attr, None)
        if not callable(getter):
            continue
        try:
            value = getter()
        except Exception:  # noqa: BLE001
            continue
        if value is not None and not inspect.isawaitable(value):
            setattr(target, name, _pairs(value))


async def capture_async(target: ResponseMetadata | None, call) -> None:
    """Copy headers/trailers from an aio call or AioRpcError into ``target``."""
    if target is None or call is None:
        return
    for attr, name in (("initial_metadata", "headers"), ("trailing_metadata", "trailers")):
        getter = getattr(call, attr, None)
        if not callable(getter):
            continue
        try:
            value = getter()
            if inspect.isawaitable(value):
                value = await value
        except Exception:  # noqa: BLE001
            continue
        if value is not None:
            setattr(target, name, _pairs(value))


class ClientConfig:
    """Operation settings shared by both client flavors and both constructors."""

    _default_timeout: float | None
    _retry_policy: RetryPolicy
    _stream_retries: bool
    _metadata: list[tuple[str, str]]

    def _configure(
        self,
        default_timeout: float | DefaultDeadline,
        retry_policy: RetryPolicy | Retries,
        stream_retries: StreamRetries,
    ) -> None:
        if default_timeout is DefaultDeadline.DISABLED:
            self._default_timeout = None
        else:
            if default_timeout is None or isinstance(default_timeout, DefaultDeadline):
                raise InvalidArgumentError(
                    "default_timeout: expected finite positive number or DefaultDeadline.DISABLED"
                )
            Budget(default_timeout)
            self._default_timeout = default_timeout
        self._retry_policy = _retry_policy(retry_policy)
        if not isinstance(stream_retries, StreamRetries):
            raise InvalidArgumentError(f"stream_retries: expected StreamRetries, got {stream_retries!r}")
        self._stream_retries = stream_retries is StreamRetries.ENABLED

    def _budget(self, timeout):
        return Budget(self._default_timeout if timeout is None else timeout)

    def _policy(self, retry: bool) -> RetryPolicy:
        return self._retry_policy if retry else _NO_RETRIES

    def _stream_policy(self, watch: bool) -> RetryPolicy:
        if watch or not self._stream_retries:
            return _NO_RETRIES
        return self._retry_policy

    def _backoff_seconds(self, attempt: int) -> float:
        policy = self._retry_policy
        return random.uniform(0, min(policy.initial_backoff * 2**attempt, policy.max_backoff))

    @staticmethod
    def _should_retry(policy: RetryPolicy, error: grpc.RpcError, attempt: int) -> bool:
        return attempt < policy.max_retries and error.code() in policy.retryable_codes

    @staticmethod
    def _retry_attempt(policy: RetryPolicy, rpc, attempt: int, error: SpiceDBError, delay: float):
        """Report a retry to the policy's callback; returns its result (maybe awaitable)."""
        if policy.on_retry is None:
            return None
        return policy.on_retry(RetryAttempt(_method_name(rpc), attempt + 1, error, delay))

    def _call_kwargs(self, call_options: CallOptions | None) -> dict[str, Any]:
        if call_options is None:
            return {"metadata": self._metadata}
        if not isinstance(call_options, CallOptions):
            raise InvalidArgumentError(f"call_options: expected CallOptions, got {call_options!r}")
        if self._metadata and any(key.lower() == "authorization" for key, _ in call_options.metadata):
            raise InvalidArgumentError(
                "call_options.metadata: authorization is set by the client's token"
            )
        kwargs: dict[str, Any] = {"metadata": list(self._metadata) + list(call_options.metadata)}
        if call_options.wait_for_ready is not None:
            kwargs["wait_for_ready"] = call_options.wait_for_ready
        if call_options.compression is not None:
            kwargs["compression"] = call_options.compression
        return kwargs


def response_target(call_options: CallOptions | None) -> ResponseMetadata | None:
    return call_options.response_metadata if call_options is not None else None
