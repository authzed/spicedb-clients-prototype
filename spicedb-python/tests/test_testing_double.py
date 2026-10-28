"""spicedb.testing: in-memory doubles with the real clients' surface and semantics."""

from __future__ import annotations

import inspect

import grpc
import pytest

from spicedb import (
    AlreadyExistsError,
    CheckResult,
    DeletePartial,
    DeleteProgress,
    FailedPreconditionError,
    Filter,
    InvalidArgumentError,
    ObjectRef,
    PermissionCheck,
    Permissionship,
    Relationship,
    SpiceDBError,
    SubjectFilter,
    SubjectRelationFilter,
    SubjectRef,
    Transaction,
    ZedToken,
    full,
)
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.sync import SpiceDBClient as SyncClient
from spicedb.testing import AsyncInMemoryClient, InMemoryClient

SHARED = [
    "write",
    "check",
    "check_bulk",
    "read_relationships",
    "delete_relationships",
    "delete_all_relationships_non_atomic",
]


def _shape(fn):
    sig = inspect.signature(fn)
    return [(p.name, p.kind, p.default) for p in sig.parameters.values()], str(sig.return_annotation)


@pytest.mark.parametrize("name", SHARED)
@pytest.mark.parametrize("double, real", [(InMemoryClient, SyncClient), (AsyncInMemoryClient, AioClient)])
def test_double_signatures_match_the_real_client(name, double, real):
    assert _shape(getattr(double, name)) == _shape(getattr(real, name))
    assert inspect.iscoroutinefunction(getattr(double, name)) == inspect.iscoroutinefunction(getattr(real, name))


def rel(text: str) -> Relationship:
    return Relationship.from_tuple(text)


def test_write_semantics():
    store = InMemoryClient([rel("document:a#viewer@user:alice")])
    with pytest.raises(AlreadyExistsError) as caught:
        store.write(Transaction().touch(rel("document:b#viewer@user:bob")).create(rel("document:a#viewer@user:alice")))
    assert caught.value.reason == "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP"
    assert caught.value.code() is grpc.StatusCode.ALREADY_EXISTS
    assert store.relationships() == [rel("document:a#viewer@user:alice")]  # atomic

    with pytest.raises(FailedPreconditionError) as caught:
        store.write(Transaction().must_not_match(Filter("document", resource_id="a")).touch(rel("document:c#viewer@user:c")))
    assert caught.value.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"

    result = store.write(
        Transaction().must_match(Filter("document", resource_id="a"))
        .touch(rel("document:a#viewer@user:alice[expiration:2030-01-01T00:00:00Z]"))
        .delete(rel("document:missing#viewer@user:x"))
        .create(rel("document:b#viewer@team:t#member"))
    )
    assert result.revision == ZedToken("1")
    assert [str(r) for r in store.relationships()] == [
        "document:a#viewer@user:alice[expiration:2030-01-01T00:00:00Z]",
        "document:b#viewer@team:t#member",
    ]
    with pytest.raises(InvalidArgumentError):
        store.write(Transaction())


def test_read_and_delete_semantics():
    store = InMemoryClient([rel(f"document:{i}#viewer@user:u") for i in range(5)] + [rel("document:9#viewer@team:t#member")])
    direct = Filter("document", subject=SubjectFilter("user", relation=SubjectRelationFilter.direct()))
    items = list(store.read_relationships(direct, consistency=full()))
    assert [i.relationship.resource_id for i in items] == ["0", "1", "2", "3", "4"]
    assert items[0].read_at == ZedToken("0")
    resumed = list(store.read_relationships(direct, consistency=full(), cursor=items[1].after_result_cursor))
    assert [i.relationship.resource_id for i in resumed] == ["2", "3", "4"]

    with pytest.raises(InvalidArgumentError):
        store.delete_relationships(direct)
    with pytest.raises(SpiceDBError) as caught:
        store.delete_relationships(direct, limit=2, partial=DeletePartial.DISALLOWED)
    assert caught.value.code() is grpc.StatusCode.INVALID_ARGUMENT
    assert len(store.relationships()) == 6

    partial = store.delete_relationships(direct, limit=2)
    assert (partial.deleted_count, partial.progress) == (2, DeleteProgress.PARTIAL)
    progress = []
    done = store.delete_all_relationships_non_atomic(direct, page_size=2, on_progress=progress.append)
    assert [p.deleted_count for p in progress] == [2, 3] and done.progress is DeleteProgress.COMPLETE
    assert store.relationships() == [rel("document:9#viewer@team:t#member")]
    with pytest.raises(FailedPreconditionError):
        store.delete_relationships(Filter("document"), partial=DeletePartial.DISALLOWED, must_match=[direct])


def test_registered_check_results():
    store = InMemoryClient()
    alice = SubjectRef(ObjectRef("user", "alice"))
    doc = ObjectRef("document", "a")
    store.set_check_result(doc, "view", alice, Permissionship.HAS_PERMISSION)
    store.set_check_result(doc, "edit", alice, CheckResult(Permissionship.CONDITIONAL_PERMISSION, ("ip",), ZedToken("")))
    view = store.check(PermissionCheck(doc, "view", alice), consistency=full())
    edit, other = store.check_bulk(
        [PermissionCheck(doc, "edit", alice), PermissionCheck(doc, "delete", alice)], consistency=full()
    )
    assert view.has_permission and view.checked_at == ZedToken("0")
    assert not edit and edit.missing_context == ("ip",)
    assert other.permissionship is Permissionship.NO_PERMISSION


async def test_async_double_shares_semantics():
    store = AsyncInMemoryClient()
    await store.write(Transaction().create(rel("document:a#viewer@user:u")))
    with pytest.raises(AlreadyExistsError):
        await store.write(Transaction().create(rel("document:a#viewer@user:u")))
    async with store.read_relationships(Filter("document"), consistency=full()) as stream:
        assert [i.relationship async for i in stream] == [rel("document:a#viewer@user:u")]
    seen = []

    async def record(progress):
        seen.append(progress.deleted_count)

    await store.delete_all_relationships_non_atomic(Filter("document"), on_progress=record)
    assert seen == [1] and store.relationships() == []
