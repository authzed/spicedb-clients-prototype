"""Example: both directions of "a conversion that cannot preserve meaning must fail".

Root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail".

The rule has two clauses that point opposite ways, and confusing them is the
failure mode either way:

1. Data the CALLER supplied that the client cannot represent must raise a typed
   error *naming what could not be converted*. The caller can see the failure
   and fix their input, so the client neither approximates the value nor drops
   it -- silently discarding it turns a caller's mistake into a silent wrong
   answer.
2. Values the SERVER supplied that the client does not recognise must NOT raise,
   and must map to the safe, non-permissive default -- never a grant. The caller
   has no input to correct here, and raising would turn a routine SpiceDB
   upgrade that adds an enum value into a client-side outage.

The last test covers clause 2, and needs a server that emits a permissionship
this client has never heard of -- which is why it stands up a stand-in rather
than using the real SpiceDB.
"""
from authzed.api.v1 import core_pb2 as core_pb2_revision
from spicedb import TransportSecurity

from spicedb import ObjectRef, SubjectRef, PermissionCheck, Caveat, SubjectFilter
import grpc
import pytest
from authzed.api.v1 import permission_service_pb2, permission_service_pb2_grpc
from spicedb import Filter, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import InvalidArgumentError

pytestmark = pytest.mark.integration


async def test_unconvertible_caveat_context_names_the_key() -> None:
    """A value with no protobuf representation fails loudly, naming the key.

    Dropping it would leave a caveat evaluating against context the caller
    believes it sent. ``Struct.update`` raises a bare ``ValueError("Unexpected
    type")`` for this, which is neither typed nor actionable, so the client
    converts key by key in order to name the offending one.
    """
    with pytest.raises(InvalidArgumentError) as excinfo:
        Caveat("only_on_tuesday", {"day": "tuesday", "impostor": object()})
    assert "impostor" in str(excinfo.value)
    assert "day" not in str(excinfo.value)


async def test_filter_with_subject_id_and_no_subject_type_is_refused() -> None:
    """A subject constraint the wire cannot express must fail, not widen.

    ``subject_id`` with no ``subject_type`` is not a narrower filter -- the wire
    format simply drops it, so the filter silently WIDENS. Applied to
    ``delete_relationships`` that is the difference between deleting alice's
    relationships and deleting every relationship on every document.
    """
    with pytest.raises(InvalidArgumentError) as excinfo:
        Filter(
            resource_type="document", subject=SubjectFilter("", subject_id="alice")
        )._to_proto()
    assert "subject_type" in str(excinfo.value)
    Filter(
        resource_type="document", subject=SubjectFilter("user", subject_id="alice")
    )._to_proto()


class _FutureServer(permission_service_pb2_grpc.PermissionsServiceServicer):
    """Answers with a permissionship from a SpiceDB newer than this client."""

    async def CheckPermission(self, request, context):
        return permission_service_pb2.CheckPermissionResponse(checked_at=core_pb2_revision.ZedToken(token="t"), permissionship=4242)


async def test_unknown_server_permissionship_neither_raises_nor_grants() -> None:
    """Clause 2: the opposite posture from the tests above.

    Raising here would break forward compatibility -- a SpiceDB rolling out a
    new enum value would make every deployed client throw on every check.
    """
    server = grpc.aio.server()
    permission_service_pb2_grpc.add_PermissionsServiceServicer_to_server(
        _FutureServer(), server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()
    try:
        async with SpiceDBClient(
            f"127.0.0.1:{port}", token="some-token", transport_security=TransportSecurity.PLAINTEXT
        ) as client:
            result = await client.check(
                PermissionCheck(
                    ObjectRef("document", "readme"),
                    "view",
                    SubjectRef(ObjectRef("user", "alice"), relation=None),
                ),
                consistency=full(),
            )
        assert not result.has_permission, (
            "an unrecognised permissionship must never be treated as a grant"
        )
    finally:
        await server.stop(None)
