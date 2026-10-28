"""Example: what tools (backup, restore, CLIs) need without the raw gRPC client.

- Paged reads expose each item's after-result cursor and read-at revision and
  resume from a cursor. Every page repeats the caller's consistency and page
  size, so a limit that is not a multiple of the page size still works.
- Import reports the server's status (ALREADY_EXISTS) when it aborts.
- Watch reports schema changes alongside checkpoints.
- Deletes can be all-or-nothing, and delete-all reports each batch.
- Bulk checks can return per-item errors next to the other results.
- A client can wrap a caller-owned channel, and any call can capture the
  server's response headers and trailers.
"""

import asyncio

import grpc
import grpc.aio
import pytest

from conftest import ENDPOINT, SCHEMA, TOKEN
from spicedb import (
    AlreadyExistsError,
    BulkItemErrors,
    CallOptions,
    DeletePartial,
    DeleteProgress,
    Filter,
    InvalidArgumentError,
    ObjectRef,
    PermissionCheck,
    Relationship,
    ResponseMetadata,
    SubjectRef,
    Transaction,
    TransportSecurity,
    WatchContent,
    code_of,
    full,
)
from spicedb import sync
from spicedb.aio import SpiceDBClient

pytestmark = pytest.mark.integration

ALICE = SubjectRef(ObjectRef("user", "alice"))


def _docs(prefix: str, n: int) -> list[Relationship]:
    return [Relationship.from_tuple(f"document:{prefix}{i}#viewer@user:alice") for i in range(n)]


async def _seed(client: SpiceDBClient, rels: list[Relationship]) -> None:
    txn = Transaction()
    for rel in rels:
        txn.touch(rel)
    await client.write(txn)


async def test_paged_reads_expose_cursors_and_resume(client: SpiceDBClient):
    await _seed(client, _docs("paged-", 7))
    every = Filter("document", resource_id_prefix="paged-")

    items = [item async for item in client.read_relationships(every, consistency=full(), page_size=2)]
    assert len(items) == 7  # four pages
    assert all(item.after_result_cursor and str(item.read_at) for item in items)

    # Resume after the third item with the same request and page size.
    resumed = [
        item.relationship
        async for item in client.read_relationships(
            every, consistency=full(), page_size=2, cursor=items[2].after_result_cursor
        )
    ]
    assert resumed == [item.relationship for item in items[3:]]

    # A limit that is not a multiple of the page size: the last page used to
    # be shrunk to the remainder, and SpiceDB rejected its cursor.
    looked_up = [
        r
        async for r in client.lookup_resources(
            resource_type="document", permission="view", subject=ALICE,
            consistency=full(), page_size=2, limit=5,
        )
    ]
    assert len(looked_up) == 5
    rest = [
        r.resource_id
        async for r in client.lookup_resources(
            resource_type="document", permission="view", subject=ALICE,
            consistency=full(), page_size=2, cursor=looked_up[-1].after_result_cursor,
        )
    ]
    assert len(rest) == 2 and not {r.resource_id for r in looked_up} & set(rest)

    with sync.SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as s:
        assert len(list(s.lookup_resources(
            resource_type="document", permission="view", subject=ALICE,
            consistency=full(), page_size=2, limit=3,
        ))) == 3


async def test_import_reports_the_servers_abort_status(client: SpiceDBClient):
    await _seed(client, _docs("dup-", 1))
    duplicate_last = _docs("imp-", 50) + _docs("dup-", 1)
    with pytest.raises(AlreadyExistsError) as caught:
        await client.import_relationships(duplicate_last, batch_size=1)
    assert code_of(caught.value) is grpc.StatusCode.ALREADY_EXISTS
    print(f"import aborted: {caught.value.server_message}")

    with sync.SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as s:
        with pytest.raises(AlreadyExistsError):
            s.import_relationships(iter(duplicate_last), batch_size=1)


async def test_watch_reports_schema_changes(client: SpiceDBClient):
    stream = client.watch(
        content=(WatchContent.RELATIONSHIPS, WatchContent.SCHEMA, WatchContent.CHECKPOINTS),
        timeout=30,
    )
    async with stream:
        events = stream.__aiter__()

        async def change_schema():
            await asyncio.sleep(0.5)
            await client.write_schema(SCHEMA + "\ndefinition tooling_marker {}\n")

        writer = asyncio.create_task(change_schema())
        try:
            while True:
                event = await asyncio.wait_for(events.__anext__(), 20)
                if event.schema_updated:
                    assert not event.updates
                    break
        finally:
            await writer
    await client.write_schema(SCHEMA)


async def test_delete_modes_and_progress(client: SpiceDBClient):
    await _seed(client, _docs("del-", 5))
    scope = Filter("document", resource_id_prefix="del-")

    with pytest.raises(InvalidArgumentError) as caught:
        await client.delete_relationships(scope, limit=2, partial=DeletePartial.DISALLOWED)
    assert caught.value.reason == "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE"
    assert len([r async for r in client.read_relationships(scope, consistency=full())]) == 5

    reports = []
    done = await client.delete_all_relationships_non_atomic(scope, page_size=2, on_progress=reports.append)
    assert [r.deleted_count for r in reports] == [2, 4, 5]
    assert done.progress is DeleteProgress.COMPLETE

    await _seed(client, _docs("del-", 3))
    whole = await client.delete_relationships(scope, partial=DeletePartial.DISALLOWED)  # server-default limit
    assert whole.deleted_count == 3 and whole.progress is DeleteProgress.COMPLETE


async def test_bulk_check_returns_item_errors(client: SpiceDBClient):
    await _seed(client, _docs("bulk-", 1))
    ok, broken = await client.check_bulk(
        [
            PermissionCheck(ObjectRef("document", "bulk-0"), "view", ALICE),
            PermissionCheck(ObjectRef("document", "bulk-0"), "no_such_permission", ALICE),
        ],
        consistency=full(),
        item_errors=BulkItemErrors.RETURN,
    )
    assert ok.has_permission and ok.error is None
    assert broken.error is not None and not broken
    print(f"item 1 failed with {broken.error.code().name}: {broken.error.server_message}")


async def test_caller_owned_channel_and_response_metadata():
    # The channel carries its own credentials; the client adds none and never closes it.
    credentials = grpc.composite_channel_credentials(
        grpc.local_channel_credentials(grpc.LocalConnectionType.LOCAL_TCP),
        grpc.access_token_call_credentials(TOKEN),
    )
    channel = grpc.aio.secure_channel(ENDPOINT, credentials)
    try:
        client = SpiceDBClient.from_channel(channel)
        captured = ResponseMetadata()
        schema = await client.read_schema(call_options=CallOptions(response_metadata=captured))
        assert "definition user" in schema.schema
        assert captured.trailers, "SpiceDB sends response metadata in trailers"
        print("trailers:", dict(captured.trailers))
        await client.close()
        assert (await SpiceDBClient.from_channel(channel).read_schema()).schema == schema.schema
    finally:
        await channel.close()
