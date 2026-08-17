"""Example: Caveated permission check (CONDITIONAL_PERMISSION).

Demonstrates the three-valued CheckResult.permissionship on a caveated
relationship checked WITHOUT its caveat context. The server cannot evaluate
the caveat ("now < 100" needs "now"), so it reports CONDITIONAL_PERMISSION
rather than guessing -- and CheckResult.has_permission MUST be False for a
Conditional result, or callers would be granting access on an unevaluated
condition. See examples/check_permission/ for the plain HAS_PERMISSION case.
"""

import pytest

from spicedb import Filter, Permissionship, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration

# A superset of conftest.SCHEMA (adds the "active" caveat and a caveated
# relation/permission) so examples that run after this one and rely on the
# fixture's default schema (viewer/editor/owner/view/edit/delete) keep
# working regardless of test collection order.
CAVEATED_SCHEMA = """\
caveat active(now int) {
    now < 100
}

definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    relation conditional_viewer: user with active
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
    permission conditional_view = conditional_viewer
}
"""


async def test_caveated_check_without_context_is_conditional(client: SpiceDBClient):
    await client.write_schema(CAVEATED_SCHEMA)

    # Grant conditional_viewer, guarded by the "active" caveat, WITHOUT
    # supplying its context ("now") at write time either.
    txn = Transaction()
    txn.touch(
        Relationship.from_triple(
            "document:caveated",
            "conditional_viewer",
            "user:alice",
            caveat_name="active",
        )
    )
    await client.write(txn)

    # Check with no context.
    rel = Relationship.from_triple(
        "document:caveated", "conditional_view", "user:alice"
    )
    result = await client.check_permission(full(), rel)

    print(
        f"alice can conditionally view document:caveated: {result.has_permission} "
        f"(permissionship={result.permissionship}, "
        f"missing_context={result.missing_context})"
    )
    assert result.permissionship == Permissionship.CONDITIONAL_PERMISSION
    # The key assertion: a Conditional result is NOT a grant.
    assert result.has_permission is False
    assert result.missing_context == ["now"]
    # checked_at is still populated on a conditional result -- the server
    # did evaluate the request, it just couldn't resolve the caveat.
    assert result.checked_at

    # check_any/check_all also treat this as not-granted -- a Conditional
    # result must never flip either of them to True.
    assert await client.check_any(full(), rel) is False
    assert await client.check_all(full(), rel) is False

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="caveated")
    )
