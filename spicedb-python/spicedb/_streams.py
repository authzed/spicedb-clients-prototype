"""Owned closeable streams; explicit cancellation releases a pending RPC."""

from __future__ import annotations
import asyncio
import threading
import time
from collections.abc import AsyncIterator, Iterator, Callable, Generator, AsyncGenerator
from typing import Generic, TypeVar

T = TypeVar("T")


class Stream(Iterator[T], Generic[T]):
    def __init__(
        self,
        factory: Callable[[Stream[T]], Generator[T, None, None]],
        on_close: Callable = lambda _: None,
    ) -> None:
        self._iterator = factory(self)
        self._call = None
        self._closed = False
        self._running = False
        self._lock = threading.RLock()
        self._on_close = on_close

    def _set_call(self, call):
        with self._lock:
            self._call = call
            if self._closed:
                call.cancel()

    def __iter__(self) -> Stream[T]:
        return self

    def __next__(self) -> T:
        with self._lock:
            if self._closed:
                raise StopIteration
            self._running = True
        try:
            value = next(self._iterator)
            if self._closed:
                raise StopIteration
            return value
        except BaseException:
            self.close()
            raise
        finally:
            self._running = False
            if self._closed:
                self._iterator.close()

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            if self._call is not None:
                self._call.cancel()
            if not self._running:
                self._iterator.close()
            self._on_close(self)

    def __enter__(self) -> Stream[T]:
        return self

    def __exit__(self, *args) -> None:
        self.close()


class AsyncStream(AsyncIterator[T], Generic[T]):
    def __init__(
        self,
        factory: Callable[[AsyncStream[T]], AsyncGenerator[T, None]],
        on_close: Callable = lambda _: None,
    ) -> None:
        self._iterator = factory(self)
        self._call = None
        self._closed = False
        self._pending: asyncio.Task | None = None
        self._on_close = on_close

    def _set_call(self, call):
        self._call = call
        if self._closed:
            call.cancel()

    def __aiter__(self) -> AsyncStream[T]:
        return self

    async def __anext__(self) -> T:
        if self._closed:
            raise StopAsyncIteration
        self._pending = asyncio.current_task()
        try:
            value = await anext(self._iterator)
            if self._closed:
                raise StopAsyncIteration
            return value
        except BaseException:
            self._closed = True
            if self._call is not None:
                self._call.cancel()
            self._on_close(self)
            raise
        finally:
            self._pending = None
            if self._closed:
                await self._iterator.aclose()

    async def aclose(self) -> None:
        if self._closed:
            return
        self._closed = True
        if self._call is not None:
            self._call.cancel()
        pending = self._pending
        if pending is not None and pending is not asyncio.current_task():
            pending.cancel()
            try:
                await pending
            except (asyncio.CancelledError, StopAsyncIteration):
                pass
        else:
            await self._iterator.aclose()
        self._on_close(self)

    async def __aenter__(self) -> AsyncStream[T]:
        return self

    async def __aexit__(self, *args) -> None:
        await self.aclose()


class Watchdog:
    """Cancel a sync call when one wait for its next message runs too long.

    ``arm`` starts a wait and ``disarm`` ends it, so time the caller spends
    holding an item never counts. One daemon thread per call; ``stop`` ends it.
    ``fired`` reports whether this watchdog cancelled the call.
    """

    def __init__(self, call) -> None:
        self._call = call
        self._cond = threading.Condition()
        self._deadline: float | None = None
        self._stopped = False
        self.fired = False
        threading.Thread(target=self._run, name="spicedb-stream-watchdog", daemon=True).start()

    def arm(self, seconds: float) -> None:
        with self._cond:
            self._deadline = time.monotonic() + seconds
            self._cond.notify()

    def disarm(self) -> None:
        with self._cond:
            self._deadline = None
            self._cond.notify()

    def stop(self) -> None:
        with self._cond:
            self._stopped = True
            self._cond.notify()

    def _run(self) -> None:
        with self._cond:
            while not self._stopped:
                if self._deadline is None:
                    self._cond.wait()
                    continue
                remaining = self._deadline - time.monotonic()
                if remaining > 0:
                    self._cond.wait(remaining)
                    continue
                self.fired = True
                self._deadline = None
                self._call.cancel()
