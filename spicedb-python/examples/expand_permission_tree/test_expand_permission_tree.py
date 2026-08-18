"""Example: Expanding a permission into its tree of subjects.

Demonstrates expand_permission_tree and walking the native PermissionTree
(expanded_object, expanded_relation, and exactly one of intermediate/leaf).
"""

import pytest

from spicedb import Filter, Relationship, SpiceDBClient, full
from spicedb.types import PermissionTree, SubjectRef, Transaction


pytestmark = pytest.mark.integration


def _collect_subjects(tree: PermissionTree) -> list[SubjectRef]:
    """Recursively walk a PermissionTree, collecting every subject found at
    its leaves. Exactly one of tree.intermediate / tree.leaf is non-None."""
    if tree.leaf is not None:
        return list(tree.leaf.subjects)
    assert tree.intermediate is not None
    subjects: list[SubjectRef] = []
    for child in tree.intermediate.children:
        subjects.extend(_collect_subjects(child))
    return subjects


async def test_expand_permission_tree(client: SpiceDBClient):
    # document:readme's "view" permission is `viewer + editor + owner`
    # (a union), so alice, bob, and carol should all show up somewhere in
    # the expanded tree.
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:readme", "editor", "user:bob"))
    txn.touch(Relationship.from_triple("document:readme", "owner", "user:carol"))
    revision = await client.write(txn)

    tree, expanded_at = await client.expand_permission_tree(
        ("document", "readme"), "view", full()
    )
    print(f"expanded at revision: {expanded_at}")
    assert expanded_at

    assert tree.expanded_object.object_type == "document"
    assert tree.expanded_object.object_id == "readme"
    assert tree.expanded_relation == "view"

    # "view" combines three relations, so the root is an intermediate node.
    assert tree.intermediate is not None
    assert tree.leaf is None
    print(f"root operation: {tree.intermediate.operation}")

    subject_ids = {s.subject_id for s in _collect_subjects(tree)}
    print(f"subjects with view access: {subject_ids}")
    assert subject_ids == {"alice", "bob", "carol"}

    # Confirm the write actually happened at (or before) the expansion.
    assert revision

    # Clean up so later tests that write a narrower schema (e.g. one
    # without an `owner` relation) aren't blocked by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="readme")
    )


async def test_expand_permission_tree_single_relation(client: SpiceDBClient):
    # document:readme's "delete" permission is just `owner` — a single
    # relation with no set operation, which SpiceDB may expand straight to
    # a leaf node rather than wrapping it in a one-child intermediate node.
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "owner", "user:carol"))
    await client.write(txn)

    tree, _ = await client.expand_permission_tree(
        ("document", "readme"), "delete", full()
    )

    subject_ids = {s.subject_id for s in _collect_subjects(tree)}
    print(f"subjects with delete access: {subject_ids}")
    assert subject_ids == {"carol"}

    # Clean up so later tests that write a narrower schema (e.g. one
    # without an `owner` relation) aren't blocked by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="readme")
    )
