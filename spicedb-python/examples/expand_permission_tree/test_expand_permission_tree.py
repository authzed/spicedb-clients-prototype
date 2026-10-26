"""Example: Expanding a permission into its tree of subjects.

Demonstrates expand_permission_tree and walking the native PermissionTree
(expanded_object, expanded_relation, and exactly one of intermediate/leaf).
"""

from spicedb import ObjectRef
import pytest
from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
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
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:readme", "editor", "user:bob"))
    txn.touch(Relationship.from_triple("document:readme", "owner", "user:carol"))
    revision = (await client.write(txn)).revision
    result = await client.expand_permission_tree(
        resource=ObjectRef("document", "readme"), permission="view", consistency=full()
    )
    tree = result.tree
    expanded_at = result.revision
    print(f"expanded at revision: {expanded_at}")
    assert expanded_at
    assert tree.expanded_object.object_type == "document"
    assert tree.expanded_object.object_id == "readme"
    assert tree.expanded_relation == "view"
    assert tree.intermediate is not None
    assert tree.leaf is None
    print(f"root operation: {tree.intermediate.operation}")
    subject_ids = {s.object.object_id for s in _collect_subjects(tree)}
    print(f"subjects with view access: {subject_ids}")
    assert subject_ids == {"alice", "bob", "carol"}
    assert str(revision)
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="readme")
    )


async def test_expand_permission_tree_single_relation(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "owner", "user:carol"))
    await client.write(txn)
    result = await client.expand_permission_tree(
        resource=ObjectRef("document", "readme"),
        permission="delete",
        consistency=full(),
    )
    tree = result.tree
    subject_ids = {s.object.object_id for s in _collect_subjects(tree)}
    print(f"subjects with delete access: {subject_ids}")
    assert subject_ids == {"carol"}
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="readme")
    )
