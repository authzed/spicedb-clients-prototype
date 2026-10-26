"""Example: Caveated permission check (CONDITIONAL_PERMISSION).

Demonstrates the three-valued CheckResult.permissionship on a caveated
relationship checked WITHOUT its caveat context. The server cannot evaluate
the caveat ("now < 100" needs "now"), so it reports CONDITIONAL_PERMISSION
rather than guessing -- and CheckResult.has_permission MUST be False for a
Conditional result, or callers would be granting access on an unevaluated
condition. See examples/check_permission/ for the plain HAS_PERMISSION case.
"""

from spicedb import ObjectRef, SubjectRef, PermissionCheck, Caveat
import pytest
from spicedb import Filter, Permissionship, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration
CAVEATED_SCHEMA = "caveat active(now int) {\n    now < 100\n}\n\ndefinition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    relation owner: user\n    relation conditional_viewer: user with active\n    permission view = viewer + editor + owner\n    permission edit = editor + owner\n    permission delete = owner\n    permission conditional_view = conditional_viewer\n}\n"


async def test_caveated_check_without_context_is_conditional(client: SpiceDBClient):
    await client.write_schema(CAVEATED_SCHEMA)
    txn = Transaction()
    txn.touch(
        Relationship.from_triple(
            "document:caveated",
            "conditional_viewer",
            "user:alice",
            caveat=Caveat("active", None),
        )
    )
    await client.write(txn)
    rel = Relationship.from_triple(
        "document:caveated", "conditional_view", "user:alice"
    )
    result = await client.check(
        PermissionCheck(rel.resource, rel.relation, rel.subject), consistency=full()
    )
    print(
        f"alice can conditionally view document:caveated: {result.has_permission} (permissionship={result.permissionship}, missing_context={result.missing_context})"
    )
    assert result.permissionship == Permissionship.CONDITIONAL_PERMISSION
    assert result.has_permission is False
    assert result.missing_context == ("now",)
    assert str(result.checked_at)
    assert (lambda results: bool(results) and any(results))(
        await client.check_bulk(
            [PermissionCheck(rel.resource, rel.relation, rel.subject)],
            consistency=full(),
        )
    ) is False
    assert (lambda results: bool(results) and all(results))(
        await client.check_bulk(
            [PermissionCheck(rel.resource, rel.relation, rel.subject)],
            consistency=full(),
        )
    ) is False
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="caveated")
    )


async def test_caveated_check_with_context_resolves_to_grant(client: SpiceDBClient):
    """The payoff test for per-item check context (spec D3b): the prior test
    proves a caveated check comes back CONDITIONAL_PERMISSION with
    missing_context=["now"] when no context is supplied. This test proves a
    caller can ACT on that -- supplying the missing "now" resolves the same
    conditional into a real HAS_PERMISSION grant.

    Uses `PermissionCheck(..., context=...)`, the new
    per-item override, rather than the call-level `context=` kwarg that
    already existed -- this is what this change actually adds.
    """
    await client.write_schema(CAVEATED_SCHEMA)
    txn = Transaction()
    txn.touch(
        Relationship.from_triple(
            "document:caveated",
            "conditional_viewer",
            "user:alice",
            caveat=Caveat("active", None),
        )
    )
    await client.write(txn)
    rel = PermissionCheck(
        ObjectRef("document", "caveated"),
        "conditional_view",
        SubjectRef(ObjectRef("user", "alice"), None),
        {"now": 50},
    )
    result = await client.check(rel, consistency=full())
    print(
        f"alice can view document:caveated with context supplied: {result.has_permission} (permissionship={result.permissionship})"
    )
    assert result.permissionship == Permissionship.HAS_PERMISSION
    assert result.has_permission is True
    assert result.missing_context == ()
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="caveated")
    )
