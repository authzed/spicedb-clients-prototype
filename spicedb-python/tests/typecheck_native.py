"""Positive and negative examples checked by mypy with warn-unused-ignores."""
from spicedb import DeleteResult, WriteResult, Transaction, LookupPermissionshipFilter, Tracing, ZedToken, at_least, TransportSecurity, RemoteCredentials

from spicedb import (
    BulkItemErrors,
    CallOptions,
    DefaultDeadline,
    DeletePartial,
    ReadRelationship,
    ResponseMetadata,
    Retries,
    RetryPolicy,
    StreamRetries,
    ObjectRef,
    SubjectRef,
    PermissionCheck,
    Relationship,
    CheckResult,
    Filter,
    full,
)
from spicedb.sync import SpiceDBClient, Stream
from spicedb.aio import SpiceDBClient as AioClient, AsyncStream

resource = ObjectRef("document", "one")
subject = SubjectRef(ObjectRef("user", "alice"))
check = PermissionCheck(resource, "view", subject, {"nested": [None, True, 3]})
relationship = Relationship(resource, "viewer", subject)


def sync_types(client: SpiceDBClient) -> None:
    result: CheckResult = client.check(
        check, consistency=full(), tracing=Tracing.ENABLED, timeout=1
    )
    results: list[CheckResult] = client.check_bulk([check], consistency=full())
    stream: Stream[ReadRelationship] = client.read_relationships(
        Filter("document"), consistency=full()
    )
    with stream as values:
        item: Relationship = next(values).relationship
        assert item == relationship
    assert result.has_permission and results
    client.check(relationship, consistency=full())  # type: ignore[arg-type]
    client.check(check)  # type: ignore[call-arg]
    client.check_permission(full(), relationship)  # type: ignore[attr-defined]


async def async_types(client: AioClient) -> None:
    result: CheckResult = await client.check(check, consistency=full())
    stream: AsyncStream[ReadRelationship] = client.read_relationships(
        Filter("document"), consistency=full()
    )
    async with stream as values:
        item: Relationship = (await anext(values)).relationship
        assert item == relationship
    assert result.has_permission


def native_token_options(client: SpiceDBClient) -> None:
    revision: ZedToken = client.write_schema("definition user {}")
    client.check(check, consistency=at_least(revision), tracing=Tracing.ENABLED)
    SpiceDBClient("remote.example:50051", token="secret", transport_security=TransportSecurity.PLAINTEXT, remote_credentials=RemoteCredentials.ALLOW)
    at_least("revision")  # type: ignore[arg-type]
    client.check(check, consistency=full(), tracing=True)  # type: ignore[arg-type]
    client.watch(start_revision="revision")  # type: ignore[arg-type]
    client.download_permission_sets(at_revision="revision")  # type: ignore[arg-type]
    SpiceDBClient("remote.example:50051", token="secret", remote_credentials=True)  # type: ignore[arg-type]


def lookup_filter_types(client: SpiceDBClient, aio: AioClient) -> None:
    client.lookup_resources(resource_type="document", permission="view", subject=subject, consistency=full(), permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
    aio.lookup_resources(resource_type="document", permission="view", subject=subject, consistency=full(), permissionship_filter=LookupPermissionshipFilter.ALL)
    client.lookup_resources(resource_type="document", permission="view", subject=subject, consistency=full(), permissionship_filter=True)  # type: ignore[arg-type]
    aio.lookup_resources(resource_type="document", permission="view", subject=subject, consistency=full(), permissionship_filter="all")  # type: ignore[arg-type]


def write_result_types(client: SpiceDBClient) -> None:
    result: WriteResult = client.write(Transaction())
    revision: ZedToken = result.revision
    at_least(revision)
    at_least(result)  # type: ignore[arg-type]
    token: ZedToken = client.write(Transaction())  # type: ignore[assignment]
    assert token


async def async_write_result_types(client: AioClient) -> None:
    result: WriteResult = await client.write(Transaction())
    at_least(result.revision)
    at_least(result)  # type: ignore[arg-type]


def non_atomic_delete_types(client: SpiceDBClient) -> None:
    result: DeleteResult = client.delete_all_relationships_non_atomic(Filter("document"))
    at_least(result.revision)
    client.delete_all_relationships(Filter("document"))  # type: ignore[attr-defined]


async def async_non_atomic_delete_types(client: AioClient) -> None:
    result: DeleteResult = await client.delete_all_relationships_non_atomic(Filter("document"))
    at_least(result.revision)
    await client.delete_all_relationships(Filter("document"))  # type: ignore[attr-defined]


def watch_filter_types(client: SpiceDBClient, aio: AioClient) -> None:
    client.watch(filters=[Filter("document", relation="viewer")])
    aio.watch(filters=[Filter("document", resource_id_prefix="team/")])
    client.watch(filters=["document"])  # type: ignore[list-item]
    aio.watch(filters=["document"])  # type: ignore[list-item]


def adoption_option_types(client: SpiceDBClient, aio: AioClient) -> None:
    SpiceDBClient("localhost:50051", token="t", default_timeout=DefaultDeadline.DISABLED, retry_policy=Retries.DISABLED, stream_retries=StreamRetries.DISABLED)
    AioClient("localhost:50051", token="t", retry_policy=RetryPolicy(max_retries=1))
    SpiceDBClient("localhost:50051", token="t", default_timeout=None)  # type: ignore[arg-type]
    SpiceDBClient("localhost:50051", token="t", retry_policy=0)  # type: ignore[arg-type]
    SpiceDBClient("localhost:50051", token="t", max_retries=0)  # type: ignore[call-arg]
    results: list[CheckResult] = client.check_bulk([check], consistency=full(), item_errors=BulkItemErrors.RETURN)
    assert results[0].error is None
    client.check_bulk([check], consistency=full(), item_errors=True)  # type: ignore[arg-type]
    client.delete_relationships(Filter("document"), partial=DeletePartial.DISALLOWED)
    client.delete_relationships(Filter("document"), partial=False)  # type: ignore[arg-type]
    captured = ResponseMetadata()
    client.read_schema(call_options=CallOptions(metadata=(("x", "y"),), response_metadata=captured))
    client.read_schema(call_options={"metadata": ()})  # type: ignore[arg-type]
    cursor: str | None = next(client.read_relationships(Filter("document"), consistency=full())).after_result_cursor
    client.read_relationships(Filter("document"), consistency=full(), cursor=cursor)
    client.import_relationships([relationship], batch_size=10)
    aio.lookup_resources(resource_type="document", permission="view", subject=subject, consistency=full(), cursor="c")
