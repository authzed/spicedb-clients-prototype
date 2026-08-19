"""Example: which calls are retried for you, and which deliberately are not.

Root DESIGN.md, "RULE: Automatic retry is for idempotent operations only".

The rule exists because a silently retried mutation produces a confident wrong
answer. If a ``WriteRelationships`` carrying ``OPERATION_CREATE`` commits and the
response is lost, the retry comes back ``ALREADY_EXISTS`` -- and the caller
concludes a write failed that in fact succeeded. Retrying reads is free;
retrying mutations is only safe when the caller opted in knowing that.

Attempts are counted *server-side*, which is the only way to tell a retry from
its absence: from the caller's side a transparently-retried success and a
first-try success are identical, and that is exactly the property that would rot
unnoticed.

It stands up a stand-in SpiceDB because a real one cannot be asked to fail
transiently on demand.
"""

import grpc
import pytest
from authzed.api.v1 import permission_service_pb2, permission_service_pb2_grpc

from spicedb import Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import ResourceExhaustedError, UnavailableError
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


DOC = Relationship(
    resource_type="document",
    resource_id="readme",
    resource_relation="view",
    subject_type="user",
    subject_id="alice",
)


class _CountingServer(permission_service_pb2_grpc.PermissionsServiceServicer):
    """Fails a configurable number of opening attempts and counts every one."""

    def __init__(self, check_failures: int = 0, check_code=grpc.StatusCode.UNAVAILABLE):
        self.check_attempts = 0
        self.write_attempts = 0
        self._check_failures = check_failures
        self._check_code = check_code

    async def CheckBulkPermissions(self, request, context):  # noqa: N802
        self.check_attempts += 1
        if self.check_attempts <= self._check_failures:
            await context.abort(self._check_code, "transient, from the stand-in")
        return permission_service_pb2.CheckBulkPermissionsResponse(
            pairs=[
                permission_service_pb2.CheckBulkPermissionsPair(
                    item=permission_service_pb2.CheckBulkPermissionsResponseItem(
                        permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
                    )
                )
                for _ in request.items
            ]
        )

    async def WriteRelationships(self, request, context):  # noqa: N802
        self.write_attempts += 1
        # Always fails, transiently. A retrying client would come back.
        await context.abort(grpc.StatusCode.UNAVAILABLE, "transient, from the stand-in")


async def _serve(servicer) -> tuple[grpc.aio.Server, int]:
    server = grpc.aio.server()
    permission_service_pb2_grpc.add_PermissionsServiceServicer_to_server(
        servicer, server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    return server, port


async def test_a_read_is_retried_transparently() -> None:
    """Two UNAVAILABLE responses, then success.

    The caller sees one successful check and never learns the first two attempts
    happened -- which is the entire value of retrying reads, and safe precisely
    because a repeated read changes nothing.
    """
    servicer = _CountingServer(check_failures=2)
    server, port = await _serve(servicer)
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="some-token", insecure=True
        ) as client:
            result = await client.check_permission(full(), DOC)
        assert result.has_permission
        assert servicer.check_attempts == 3, (
            f"expected 2 failures plus 1 success = 3 attempts, got "
            f"{servicer.check_attempts} (0 or 1 means reads are not retried at all)"
        )
    finally:
        await server.stop(None)


async def test_a_mutation_is_not_retried() -> None:
    """The same transient code, on a write, reaches the caller immediately.

    The caller -- who alone knows whether a replay is safe for the transaction
    they built -- decides what happens next. Exactly one attempt is the
    assertion that matters here.
    """
    servicer = _CountingServer()
    server, port = await _serve(servicer)
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="some-token", insecure=True
        ) as client:
            txn = Transaction()
            txn.touch(
                Relationship(
                    resource_type="document",
                    resource_id="readme",
                    resource_relation="viewer",
                    subject_type="user",
                    subject_id="alice",
                )
            )
            with pytest.raises(UnavailableError):
                await client.write(txn)
        assert servicer.write_attempts == 1, (
            f"a mutation must not be retried silently: WriteRelationships saw "
            f"{servicer.write_attempts} attempts, so a lost response would leave "
            "the caller believing a committed write had failed"
        )
    finally:
        await server.stop(None)


async def test_resource_exhausted_is_not_retried_even_on_a_read() -> None:
    """In SpiceDB this code means load-shed or a deterministic MaxDepthExceeded.

    Retrying the first makes the overload worse; the second can never succeed
    however many times it is tried. So it is deliberately absent from the
    retryable set even though the call itself is a read.
    """
    servicer = _CountingServer(
        check_failures=99, check_code=grpc.StatusCode.RESOURCE_EXHAUSTED
    )
    server, port = await _serve(servicer)
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="some-token", insecure=True
        ) as client:
            with pytest.raises(ResourceExhaustedError):
                await client.check_permission(full(), DOC)
        assert servicer.check_attempts == 1, (
            f"RESOURCE_EXHAUSTED must not be retried: saw {servicer.check_attempts} "
            "attempts, which turns a load-shedding SpiceDB into a client-driven "
            "retry storm"
        )
    finally:
        await server.stop(None)
