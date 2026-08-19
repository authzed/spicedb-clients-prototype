"""Example: reaching past the idiomatic API with `raw_grpc()`.

Every wrapper eventually meets a request the wrapper does not express. This
client's answer is `raw_grpc()`: the live channel plus the bearer-token
metadata, from which you can build any generated stub and call anything SpiceDB
serves -- a workaround short of forking the client. Root DESIGN.md, "What NOT
To Do", allows exactly this as "clearly marked secondary API".

The gap demonstrated here is real, not hypothetical:
`WriteRelationshipsRequest.optional_transaction_metadata` is a proto field this
client does not surface anywhere. Applications use it to stamp an audit
correlation ID onto a write, which comes back out of the Watch stream. Below,
the raw stub sends it; the idiomatic client then reads the same relationship
back over the same connection.

What you give up on the raw path, and why the idiomatic methods stay the
default: no `spicedb.errors` mapping (you catch `grpc.RpcError`), no retry on a
transient failure, and no `default_timeout` -- pass `timeout=` yourself or the
call is unbounded. `raw_grpc()` also makes no stability promise beyond grpc's
own.
"""

import pytest
from authzed.api.v1 import core_pb2 as core
from authzed.api.v1 import permission_service_pb2 as psp
from authzed.api.v1 import permission_service_pb2_grpc as psg
from google.protobuf import struct_pb2

from conftest import ENDPOINT, SCHEMA, TOKEN
from spicedb import Filter, Relationship, full
from spicedb.consistency import at_least
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_raw_grpc_sends_a_field_the_idiomatic_api_does_not_expose(client):
    """Async flavor: `raw_grpc()` is awaited, since a `grpc.aio` channel is
    created on -- and bound to -- the running event loop."""
    await client.write_schema(SCHEMA)

    raw = await client.raw_grpc()
    stub = psg.PermissionsServiceStub(raw.channel)

    metadata = struct_pb2.Struct()
    metadata.update({"correlation_id": "example-42", "actor": "billing-job"})

    request = psp.WriteRelationshipsRequest(
        updates=[
            core.RelationshipUpdate(
                operation=core.RelationshipUpdate.OPERATION_TOUCH,
                relationship=core.Relationship(
                    resource=core.ObjectReference(
                        object_type="document", object_id="ledger"
                    ),
                    relation="viewer",
                    subject=core.SubjectReference(
                        object=core.ObjectReference(
                            object_type="user", object_id="jimmy"
                        )
                    ),
                ),
            )
        ],
        optional_transaction_metadata=metadata,
    )

    # `metadata=raw.metadata` is what authenticates this call: the client
    # attaches its bearer token per call rather than on the channel, so a stub
    # built from `raw.channel` alone would get UNAUTHENTICATED.
    response = await stub.WriteRelationships(request, metadata=raw.metadata)
    revision = response.written_at.token
    print(f"raw write committed at revision {revision}")
    assert revision

    # Same client, same connection: the idiomatic API picks up right where the
    # raw call left off, including read-your-writes on the raw revision.
    result = await client.check_permission(
        at_least(revision),
        Relationship.from_triple("document:ledger", "view", "user:jimmy"),
    )
    print(f"user:jimmy can view document:ledger: {result.has_permission}")
    assert result.has_permission is True

    await client.delete_relationships(
        Filter(resource_type="document", resource_id="ledger")
    )


def test_raw_grpc_from_the_sync_client():
    """Sync flavor: same hatch, no `await` -- `raw_grpc()` returns directly."""
    sync_client = SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    )
    try:
        sync_client.write_schema(SCHEMA)
        txn = Transaction()
        txn.touch(Relationship.from_triple("document:ledger", "viewer", "user:jimmy"))
        sync_client.write(txn)

        raw = sync_client.raw_grpc()
        stub = psg.PermissionsServiceStub(raw.channel)

        # `CheckPermission` -- the single-check RPC. The idiomatic
        # `check_permission()` routes every check through
        # `CheckBulkPermissions`, so this is the way to drive the unary RPC
        # itself.
        request = psp.CheckPermissionRequest(
            consistency=psp.Consistency(fully_consistent=True),
            resource=core.ObjectReference(object_type="document", object_id="ledger"),
            permission="view",
            subject=core.SubjectReference(
                object=core.ObjectReference(object_type="user", object_id="jimmy")
            ),
        )
        # A raw call gets no client default deadline -- pass one yourself.
        response = stub.CheckPermission(request, metadata=raw.metadata, timeout=30.0)
        print(f"raw CheckPermission permissionship: {response.permissionship}")
        assert (
            response.permissionship
            == psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
        )

        # The channel belongs to the client: `close()` below closes it, and
        # closing it here would break every later call. Nothing to clean up
        # from the hatch itself.
        assert sync_client.check_permission(
            full(), Relationship.from_triple("document:ledger", "view", "user:jimmy")
        ).has_permission

        sync_client.delete_relationships(
            Filter(resource_type="document", resource_id="ledger")
        )
    finally:
        sync_client.close()
