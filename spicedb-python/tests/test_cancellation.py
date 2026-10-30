"""Local cancellation surfaces as the client's own cancelled error.

Root DESIGN.md: "Local cancellation surfaces as the client's cancelled error,
never as the platform's own abort exception", and "Python propagates task
cancellation". So closing a stream (from another thread or task) while a
consumer waits on it raises ``spicedb.errors.CancelledError`` in that
consumer -- never a raw ``grpc.RpcError`` or ``asyncio.CancelledError`` --
while cancelling the consumer's own task still propagates
``asyncio.CancelledError``, as asyncio requires.
"""

from __future__ import annotations

import asyncio
import threading
import time

import grpc
import pytest

from spicedb import TransportSecurity, code_of, full
from spicedb.errors import CancelledError, SpiceDBError
from tests.test_stream_release import (  # noqa: F401 -- parking_server is a fixture
    TOKEN,
    AioClient,
    SyncClient,
    _assert_released,
    _subject,
    parking_server,
)

PLAIN = TransportSecurity.PLAINTEXT


def _streams(client):
    return {
        "watch": lambda: client.watch(),
        "lookup_resources": lambda: client.lookup_resources(
            resource_type="document", permission="view", subject=_subject("user:u"), consistency=full()
        ),
    }


def _assert_client_cancelled(error: BaseException) -> None:
    assert isinstance(error, CancelledError), repr(error)
    assert isinstance(error, SpiceDBError) and isinstance(error, grpc.RpcError)
    assert error.code() is grpc.StatusCode.CANCELLED
    assert code_of(error) is grpc.StatusCode.CANCELLED


@pytest.mark.parametrize("name", ["watch", "lookup_resources"])
def test_sync_close_from_another_thread_raises_the_clients_cancelled_error(parking_server, name):  # noqa: F811 -- the imported fixture
    port, parked = parking_server
    client = SyncClient(f"localhost:{port}", token=TOKEN, transport_security=PLAIN)
    stream = _streams(client)[name]()
    next(stream)
    observed: list[BaseException] = []

    def consume():
        try:
            next(stream)
        except BaseException as error:  # noqa: BLE001 -- recorded for the assertion
            observed.append(error)

    worker = threading.Thread(target=consume)
    worker.start()
    time.sleep(0.05)
    stream.close()
    worker.join(2)
    try:
        assert not worker.is_alive()
        _assert_client_cancelled(observed[0])
        _assert_released(parked)
    finally:
        client.close()


@pytest.mark.parametrize("name", ["watch", "lookup_resources"])
async def test_aio_aclose_from_another_task_raises_the_clients_cancelled_error(parking_server, name):  # noqa: F811 -- the imported fixture
    """The consumer's task is not cancelled: it sees the stream's error and
    keeps running."""
    port, parked = parking_server
    async with AioClient(f"localhost:{port}", token=TOKEN, transport_security=PLAIN) as client:
        stream = _streams(client)[name]()
        await anext(stream)
        waiting = asyncio.Event()

        async def consumer():
            waiting.set()
            try:
                await anext(stream)
            except CancelledError as error:
                await asyncio.sleep(0)  # the task is still usable afterwards
                return error
            return None

        task = asyncio.create_task(consumer())
        await waiting.wait()
        await asyncio.sleep(0.05)
        await stream.aclose()
        error = await asyncio.wait_for(task, 2)
        assert not task.cancelled()
        assert task.cancelling() == 0
        _assert_client_cancelled(error)
        _assert_released(parked)
        with pytest.raises(StopAsyncIteration):
            await anext(stream)


async def test_aio_task_cancellation_still_propagates_natively(parking_server):  # noqa: F811 -- the imported fixture
    port, parked = parking_server
    async with AioClient(f"localhost:{port}", token=TOKEN, transport_security=PLAIN) as client:
        stream = client.watch()
        await anext(stream)
        task = asyncio.create_task(anext(stream))
        await asyncio.sleep(0.05)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert code_of(asyncio.CancelledError()) is grpc.StatusCode.CANCELLED
        _assert_released(parked)


async def test_aio_task_cancelled_during_aclose_still_propagates(parking_server):  # noqa: F811 -- the imported fixture
    """When the caller cancels the task too, asyncio's cancellation wins."""
    port, parked = parking_server
    async with AioClient(f"localhost:{port}", token=TOKEN, transport_security=PLAIN) as client:
        stream = client.watch()
        await anext(stream)
        task = asyncio.create_task(anext(stream))
        await asyncio.sleep(0.05)
        task.cancel()
        await stream.aclose()
        with pytest.raises(asyncio.CancelledError):
            await task
