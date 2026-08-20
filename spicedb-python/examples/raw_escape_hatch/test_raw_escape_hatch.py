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

import asyncio

import pytest
from authzed.api.v1 import core_pb2 as core
from authzed.api.v1 import permission_service_pb2 as psp
from authzed.api.v1 import permission_service_pb2_grpc as psg
from authzed.api.v1 import watch_service_pb2 as wsp
from authzed.api.v1 import watch_service_pb2_grpc as wsg
from google.protobuf import struct_pb2

from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, full
from spicedb.consistency import at_least
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_raw_grpc_sends_a_field_the_idiomatic_api_does_not_expose(client):
    """Async flavor: `raw_grpc()` is awaited, since a `grpc.aio` channel is
    created on -- and bound to -- the running event loop."""
    await client.write_schema(SCHEMA)

    # A seed write fixes the revision the Watch below starts from, so it sees
    # the metadata write and nothing that came before it. (The `client` fixture
    # clears `document` first, so the raw write below is a real change -- a
    # TOUCH of an already-identical relationship produces no watch event.)
    seed = Transaction()
    seed.touch(Relationship.from_triple("document:ledger", "viewer", "user:seed"))
    seed_revision = await client.write(seed)

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

    # Read the metadata back. Sending it proves nothing on its own: a client
    # that dropped the field would look identical from here, because
    # WriteRelationships does not echo it back. The only place it becomes
    # observable is the Watch stream -- and `optional_transaction_metadata` is
    # not on the idiomatic `WatchEvent` either, so the read-back is a second
    # use of the same escape hatch.
    watch_stub = wsg.WatchServiceStub(raw.channel)
    watch_call = watch_stub.Watch(
        wsp.WatchRequest(
            optional_object_types=["document"],
            optional_start_cursor=core.ZedToken(token=seed_revision),
        ),
        metadata=raw.metadata,
    )
    seen_metadata = None
    try:
        async with asyncio.timeout(30.0):
            async for response in watch_call:
                if response.HasField("optional_transaction_metadata"):
                    seen_metadata = dict(response.optional_transaction_metadata)
                    break
    except TimeoutError:
        pytest.fail(
            "no watch event carried optional_transaction_metadata within 30s: "
            "the metadata sent on the raw write never reached the server"
        )
    finally:
        # Abandoning a raw stream is the caller's job on this path: the hatch
        # hands back the generated stub, so there is no iterator cleanup to
        # lean on. See root DESIGN.md, "RULE: Abandoning a stream must release
        # it".
        watch_call.cancel()

    print(f"watch reported transaction metadata: {seen_metadata}")
    assert seen_metadata == {
        "correlation_id": "example-42",
        "actor": "billing-job",
    }

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
        # Clear before writing the schema -- see conftest.clear_documents.
        clear_documents_sync(sync_client)
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
