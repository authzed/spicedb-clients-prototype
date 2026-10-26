"""Canonical checks, tracing, bulk results, and safe native aggregation."""
from spicedb import Tracing

import pytest
from spicedb import (
    ObjectRef,
    SubjectRef,
    PermissionCheck,
    Relationship,
    Transaction,
    Filter,
    full,
    at_least,
)
from spicedb.aio import SpiceDBClient

pytestmark = pytest.mark.integration


async def test_check_permission(client: SpiceDBClient):
    resource = ObjectRef("document", "readme")
    alice = SubjectRef(ObjectRef("user", "alice"))
    bob = SubjectRef(ObjectRef("user", "bob"))
    transaction = (
        Transaction()
        .touch(Relationship(resource, "viewer", alice))
        .touch(Relationship(resource, "editor", bob))
    )
    revision = (await client.write(transaction)).revision
    check = PermissionCheck(resource, "view", alice)
    result = await client.check(check, consistency=full(), tracing=Tracing.ENABLED)
    assert result.has_permission and bool(result)
    assert str(result.checked_at)
    assert result.debug_trace is not None
    assert "definition document" in result.debug_trace.schema_used
    assert result.debug_trace.check is not None
    assert result.debug_trace.check.permission == "view"
    fresh = await client.check(check, consistency=at_least(revision))
    assert fresh.has_permission
    checks = [
        check,
        PermissionCheck(resource, "edit", alice),
        PermissionCheck(resource, "edit", bob),
    ]
    results = await client.check_bulk(checks, consistency=full(), tracing=Tracing.ENABLED)
    assert [result.has_permission for result in results] == [True, False, True]
    assert bool(results) and any(results)
    assert not (bool(results) and all(results))
    empty = await client.check_bulk([], consistency=full())
    assert not (bool(empty) and all(empty))
    await client.delete_all_relationships_non_atomic(Filter("document", resource_id="readme"))
