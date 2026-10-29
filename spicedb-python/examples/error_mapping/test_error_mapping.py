"""Example: the two error codes a caller actually recovers from.

Root DESIGN.md, "RULE: Error mapping must not lose the server's detail".

The rule names both consequences, and this example is those two recoveries
written out as running code:

- ``OUT_OF_RANGE`` is SpiceDB's signal that a ZedToken has expired or been
  garbage-collected. Recovery is mechanical: discard the stale token and re-read
  at full consistency. Collapsed into a generic error, every caller would have to
  string-match a message to recover something the client already knew the shape
  of.
- ``UNAUTHENTICATED`` is the most common error a new integration produces -- a
  wrong, expired or rotated token. Distinguishing it is what lets a caller write
  "refresh credentials on auth failure, page someone on internal error".

Why this example stands up its own server
-----------------------------------------
Neither code is reachable from the SpiceDB the integration job starts, which was
verified rather than assumed:

- A garbage ZedToken returns ``INVALID_ARGUMENT`` ("invalid revision
  requested"), not ``OUT_OF_RANGE``. A real ``OUT_OF_RANGE`` needs a revision
  that was valid and has since been collected, and the in-memory datastore does
  not collect it: with ``--datastore-gc-window=5s`` and 35 seconds elapsed, a
  snapshot read at the old token still succeeded.
- A wrong preshared key comes back ``PERMISSION_DENIED`` from SpiceDB, not
  ``UNAUTHENTICATED``. The last test asserts that against the real server, so
  this example records what SpiceDB actually does rather than what one might
  assume -- writing a credential-refresh branch on ``UnauthenticatedError`` for
  that case would leave it unreachable.
"""
from authzed.api.v1 import core_pb2 as core_pb2_revision
from spicedb import TransportSecurity

from spicedb import ObjectRef, SubjectRef, PermissionCheck
import grpc
import pytest
from authzed.api.v1 import permission_service_pb2, permission_service_pb2_grpc
from conftest import ENDPOINT
from spicedb import ZedToken, Relationship, at_least, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import (
    OutOfRangeError,
    PermissionDeniedError,
    UnauthenticatedError,
    UnavailableError,
)

pytestmark = pytest.mark.integration
STALE_TOKEN = "stale-zedtoken"
DOC = Relationship(
    ObjectRef("document", "readme"),
    "view",
    SubjectRef(ObjectRef("user", "alice"), relation=None),
)


class _StandIn(permission_service_pb2_grpc.PermissionsServiceServicer):
    """A minimal SpiceDB that answers only what this example asks of it."""

    async def CheckPermission(self, request, context):
        if request.consistency.at_least_as_fresh.token == STALE_TOKEN:
            await context.abort(
                grpc.StatusCode.OUT_OF_RANGE,
                "the specified revision has expired or been garbage collected",
            )
        return permission_service_pb2.CheckPermissionResponse(checked_at=core_pb2_revision.ZedToken(token="t"),
            permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
        )


class _RotatedTokenServer(permission_service_pb2_grpc.PermissionsServiceServicer):
    """Rejects every call the way a rotated token would."""

    async def CheckPermission(self, request, context):
        await context.abort(grpc.StatusCode.UNAUTHENTICATED, "invalid token")


async def _serve(servicer) -> tuple[grpc.aio.Server, int]:
    server = grpc.aio.server()
    permission_service_pb2_grpc.add_PermissionsServiceServicer_to_server(
        servicer, server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    return (server, port)


async def test_stale_zedtoken_is_recoverable_without_parsing_a_message() -> None:
    server, port = await _serve(_StandIn())
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="some-token", transport_security=TransportSecurity.PLAINTEXT
        ) as client:
            with pytest.raises(OutOfRangeError) as excinfo:
                await client.check(
                    PermissionCheck(DOC.resource, DOC.relation, DOC.subject),
                    consistency=at_least(ZedToken(STALE_TOKEN)),
                )
            assert excinfo.value.__cause__ is not None
            result = await client.check(
                PermissionCheck(DOC.resource, DOC.relation, DOC.subject),
                consistency=full(),
            )
            assert result.has_permission
    finally:
        await server.stop(None)


async def test_rotated_token_is_distinct_from_a_transport_fault() -> None:
    server, port = await _serve(_RotatedTokenServer())
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="rotated-token", transport_security=TransportSecurity.PLAINTEXT
        ) as client:
            with pytest.raises(UnauthenticatedError) as excinfo:
                await client.check(
                    PermissionCheck(DOC.resource, DOC.relation, DOC.subject),
                    consistency=full(),
                )
            assert not isinstance(excinfo.value, UnavailableError)
    finally:
        await server.stop(None)


async def test_real_spicedb_rejects_a_bad_preshared_key_with_permission_denied() -> (
    None
):
    """What the real server actually does -- recorded, not assumed.

    ``PERMISSION_DENIED``, not ``UNAUTHENTICATED``. This is the case a reader
    hits first, and assuming otherwise is how a credential-refresh branch ends up
    unreachable in production code.
    """
    async with SpiceDBClient(
        ENDPOINT, token="definitely-the-wrong-key", transport_security=TransportSecurity.PLAINTEXT
    ) as client:
        with pytest.raises(PermissionDeniedError):
            await client.read_schema()
