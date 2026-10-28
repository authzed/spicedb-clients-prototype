"""Example: Bulk checks and imports.

Demonstrates bulk permission checks and bulk relationship import/export.
"""

from spicedb import PermissionCheck
import pytest
from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.consistency import at_least
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_bulk_checks(client: SpiceDBClient):
    users = ["alice", "bob", "charlie"]
    txn = Transaction()
    for user in users:
        txn.touch(Relationship.from_triple("document:report", "viewer", f"user:{user}"))
    revision = (await client.write(txn)).revision
    print(f"wrote {len(users)} relationships at revision: {revision}")
    checks = [
        Relationship.from_triple("document:report", "view", f"user:{user}")
        for user in users
    ]
    results = await client.check_bulk(
        [
            *[
                PermissionCheck(item.resource, item.relation, item.subject)
                for item in checks
            ]
        ],
        consistency=at_least(revision),
    )
    for user, result in zip(users, results):
        print(f"user:{user} can view document:report: {result.has_permission}")
        assert result.has_permission is True
    all_allowed = (lambda results: bool(results) and all(results))(
        await client.check_bulk(
            [
                *[
                    PermissionCheck(item.resource, item.relation, item.subject)
                    for item in checks
                ]
            ],
            consistency=at_least(revision),
        )
    )
    print(f"all users can view: {all_allowed}")
    assert all_allowed is True
    any_allowed = (lambda results: bool(results) and any(results))(
        await client.check_bulk(
            [
                *[
                    PermissionCheck(item.resource, item.relation, item.subject)
                    for item in checks
                ]
            ],
            consistency=at_least(revision),
        )
    )
    print(f"any user can view: {any_allowed}")
    assert any_allowed is True
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="report")
    )


async def test_bulk_import_export(client: SpiceDBClient):
    users = ["dana", "erin", "frank"]
    relationships = [
        Relationship.from_triple("document:archive", "viewer", f"user:{user}")
        for user in users
    ]
    num_loaded = await client.import_relationships(relationships)
    print(f"imported {num_loaded} relationships")
    assert num_loaded == len(users)
    more_users = ["grace", "heidi"]

    def relationship_generator():
        for user in more_users:
            yield Relationship.from_triple("document:archive", "viewer", f"user:{user}")

    num_loaded_from_generator = await client.import_relationships(
        relationship_generator()
    )
    print(f"imported {num_loaded_from_generator} relationships from a generator")
    assert num_loaded_from_generator == len(more_users)
    filter = Filter(resource_type="document", resource_id="archive")
    exported = [
        item.relationship async for item in client.export_relationships(filter, consistency=full())
    ]
    print(f"exported {len(exported)} relationships")
    exported_subject_ids = {rel.subject.object.object_id for rel in exported}
    for user in users:
        assert user in exported_subject_ids
    await client.delete_all_relationships_non_atomic(filter)
