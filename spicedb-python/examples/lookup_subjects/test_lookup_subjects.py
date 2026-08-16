"""Example: Subject lookup.

Demonstrates finding subjects with access to a resource, including the
wildcard/excluded-subjects case: when the server resolves a wildcard "*"
subject, ``excluded_subjects`` lists the subjects carved out of that
wildcard grant. Treating a wildcard match as "everyone has access" without
checking ``excluded_subjects`` is a real over-grant risk.
"""

import pytest

from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_lookup_subjects(client: SpiceDBClient):
    # Set up relationships
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))
    await client.write(txn)

    # Find all users who can view document:firstdoc
    subject_ids = []
    async for result in client.lookup_subjects(
        ("document", "firstdoc"), "view", "user", full()
    ):
        print(f"user:{result.subject.subject_id} can view document:firstdoc")
        subject_ids.append(result.subject.subject_id)

    assert "alice" in subject_ids
    assert "bob" in subject_ids  # editors can also view

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="firstdoc")
    )


async def test_lookup_subjects_wildcard_excludes_banned(client: SpiceDBClient):
    # A schema where `view` is granted to every user via a wildcard, except
    # those in `banned`. This is the over-grant-risk case: seeing subject_id
    # "*" alone would suggest "every user", but excluded_subjects carves
    # specific subjects back out.
    await client.write_schema(
        """\
definition user {}

definition document {
    relation viewer: user | user:*
    relation editor: user
    relation owner: user
    relation banned: user
    permission view = (viewer + editor + owner) - banned
    permission edit = editor + owner
    permission delete = owner
}
"""
    )

    txn = Transaction()
    txn.touch(Relationship.from_triple("document:seconddoc", "editor", "user:bob"))
    # Grant view to every user (wildcard), except those in `banned`.
    txn.touch(Relationship.from_triple("document:seconddoc", "viewer", "user:*"))
    txn.touch(Relationship.from_triple("document:seconddoc", "banned", "user:eve"))
    await client.write(txn)

    saw_wildcard = False
    excluded_ids: set[str] = set()
    async for result in client.lookup_subjects(
        ("document", "seconddoc"), "view", "user", full()
    ):
        print(
            f"subject:{result.subject.subject_id} can view document:seconddoc "
            f"(permissionship={result.subject.permissionship})"
        )
        if result.subject.subject_id == "*":
            saw_wildcard = True
            # Never grant access based on the wildcard match alone — check
            # excluded_subjects first.
            for excluded in result.excluded_subjects:
                print(f"  excluded from wildcard: user:{excluded.subject_id}")
                excluded_ids.add(excluded.subject_id)

    assert saw_wildcard, "expected a wildcard (*) subject in results"
    assert "eve" in excluded_ids, "expected eve to be excluded from the wildcard grant"

    # Clean up so later tests that write a narrower schema (e.g. one
    # without a `banned` relation) aren't blocked by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="seconddoc")
    )
