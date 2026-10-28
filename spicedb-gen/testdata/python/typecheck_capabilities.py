"""Positive static contracts for direct native capabilities on typed clients."""
from collections.abc import AsyncIterable, Iterable
from spicedb import (
    Filter, DeleteResult, WatchEvent, Relationship, ExportedRelationship, PermissionCheck, CheckResult,
    WriteResult, Transaction, CountResult, SchemaResult, PermissionSetLookup,
    PermissionSetCursor, ZedToken, full, WatchContent, LookupDebug, Wildcards,
)
from spicedb.sync import Stream
from spicedb.aio import AsyncStream
from testdata.sync import TypedClient
from testdata.aio import TypedClient as AioTypedClient
from testdata.permissions import Document, DOCUMENT_VIEW, User


def native_capabilities(client: TypedClient, producer: Iterable[Relationship], cursor: PermissionSetCursor) -> None:
    filter = Filter("document")
    bounded: DeleteResult = client.delete_relationships(filter, limit=1)
    all_deleted: DeleteResult = client.delete_all_relationships_non_atomic(filter, page_size=2)
    watched: Stream[WatchEvent] = client.watch(filters=[filter], content=[WatchContent.RELATIONSHIPS])
    checked: list[CheckResult] = client.check_bulk([], consistency=full())
    written: WriteResult = client.write(Transaction(), metadata={"typed": True})
    schema: SchemaResult = client.read_schema()
    imported: int = client.import_relationships(producer)
    exported: Stream[ExportedRelationship] = client.export_relationships(filter, consistency=full())
    counted: CountResult = client.experimental_count_relationships_by_filter(filter)
    sets: Stream[PermissionSetLookup] = client.lookup_permission_sets(cursor=cursor, at_revision=ZedToken("revision"))
    client.lookup_resources(full(), DOCUMENT_VIEW, User("alice"), limit=2, page_size=1, debug=LookupDebug.ENABLED)
    client.lookup_subjects(full(), Document("one").view, User, limit=2, wildcards=Wildcards.EXCLUDE)
    assert bounded and all_deleted and watched and checked and written and schema and imported and exported and counted and sets


async def async_native_capabilities(client: AioTypedClient, producer: AsyncIterable[Relationship], checks: list[PermissionCheck]) -> None:
    filter = Filter("document")
    bounded: DeleteResult = await client.delete_relationships(filter, limit=1)
    all_deleted: DeleteResult = await client.delete_all_relationships_non_atomic(filter, page_size=2)
    watched: AsyncStream[WatchEvent] = client.watch(filters=[filter])
    checked: list[CheckResult] = await client.check_bulk(checks, consistency=full())
    written: WriteResult = await client.write(Transaction())
    imported: int = await client.import_relationships(producer)
    exported: AsyncStream[ExportedRelationship] = client.export_relationships(filter, consistency=full())
    counted: CountResult = await client.experimental_count_relationships_by_filter(filter)
    sets: AsyncStream[PermissionSetLookup] = client.lookup_permission_sets()
    assert bounded and all_deleted and watched and checked and written and imported and exported and counted and sets
