"""Positive and negative examples checked by mypy with warn-unused-ignores."""
from spicedb import DeleteResult, WriteResult, Transaction, LookupPermissionshipFilter, Tracing, ZedToken, at_least, TransportSecurity, RemoteCredentials

from spicedb import (
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
    stream: Stream[Relationship] = client.read_relationships(
        Filter("document"), consistency=full()
    )
    with stream as values:
        item: Relationship = next(values)
        assert item == relationship
    assert result.has_permission and results
    client.check(relationship, consistency=full())  # type: ignore[arg-type]
    client.check(check)  # type: ignore[call-arg]
    client.check_permission(full(), relationship)  # type: ignore[attr-defined]


async def async_types(client: AioClient) -> None:
    result: CheckResult = await client.check(check, consistency=full())
    stream: AsyncStream[Relationship] = client.read_relationships(
        Filter("document"), consistency=full()
    )
    async with stream as values:
        item: Relationship = await anext(values)
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
