"""Example: Subject lookup.

Demonstrates finding subjects with access to a resource, including the
wildcard/excluded-subjects case: when the server resolves a wildcard "*"
subject, ``excluded_subjects`` lists the subjects carved out of that
wildcard grant. Treating a wildcard match as "everyone has access" without
checking ``excluded_subjects`` is a real over-grant risk.
"""

from spicedb import ObjectRef
import pytest
from spicedb import Filter, Relationship, Permissionship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_lookup_subjects(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))
    await client.write(txn)
    subject_ids = []
    async for result in client.lookup_subjects(
        resource=ObjectRef("document", "firstdoc"),
        permission="view",
        subject_type="user",
        consistency=full(),
    ):
        # Only concrete unconditional grants belong in an accessible-ID list.
        if (result.subject.permissionship is not Permissionship.HAS_PERMISSION
                or result.subject.subject_id == "*" or result.excluded_subjects):
            continue
        print(f"user:{result.subject.subject_id} can view document:firstdoc")
        subject_ids.append(result.subject.subject_id)
    assert "alice" in subject_ids
    assert "bob" in subject_ids
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="firstdoc")
    )


async def test_lookup_subjects_wildcard_excludes_banned(client: SpiceDBClient):
    await client.write_schema(
        "definition user {}\n\ndefinition document {\n    relation viewer: user | user:*\n    relation editor: user\n    relation owner: user\n    relation banned: user\n    permission view = (viewer + editor + owner) - banned\n    permission edit = editor + owner\n    permission delete = owner\n}\n"
    )
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:seconddoc", "editor", "user:bob"))
    txn.touch(Relationship.from_triple("document:seconddoc", "viewer", "user:*"))
    txn.touch(Relationship.from_triple("document:seconddoc", "banned", "user:eve"))
    await client.write(txn)
    saw_wildcard = False
    excluded_ids: set[str] = set()
    async for result in client.lookup_subjects(
        resource=ObjectRef("document", "seconddoc"),
        permission="view",
        subject_type="user",
        consistency=full(),
    ):
        print(
            f"subject:{result.subject.subject_id} lookup match for document:seconddoc (permissionship={result.subject.permissionship})"
        )
        if result.subject.subject_id == "*":
            saw_wildcard = True
            for excluded in result.excluded_subjects:
                print(f"  excluded from wildcard: user:{excluded.subject_id}")
                excluded_ids.add(excluded.subject_id)
    assert saw_wildcard, "expected a wildcard (*) subject in results"
    assert "eve" in excluded_ids, "expected eve to be excluded from the wildcard grant"
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="seconddoc")
    )
