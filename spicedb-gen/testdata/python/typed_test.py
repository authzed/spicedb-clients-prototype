"""Integration tests for the generated permissions.py. Requires SpiceDB at localhost:50051."""

from __future__ import annotations

import pytest
from spicedb import full, Filter

from permissions import (
    TypedClient,
    TypedTransaction,
    Document,
    User,
    Team,
    TeamMember,
    IpRangeContext,
    TimeWindowContext,
    DocumentView,
)

SCHEMA = """
caveat ip_range(allowed_cidr string) {
    allowed_cidr == "0.0.0.0/0"
}

caveat time_window(start string, end string) {
    start != "" && end != ""
}

definition user {}

definition team {
    relation member: user | team#member
}

definition document {
    relation viewer: user | user with ip_range | user with time_window | team#member
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}
"""


@pytest.fixture
async def tc():
    client = TypedClient.connect("localhost:50051", "somerandomkeyhere", insecure=True)
    try:
        await client.client.write_schema(SCHEMA)
        yield client
    finally:
        await client.close()


@pytest.mark.integration
async def test_touch_and_check(tc: TypedClient) -> None:
    """Test touch shortcut to write multiple relationships and check permissions."""
    await tc.touch(
        Document("readme").viewer(User("alice")),
        Document("readme").editor(User("bob")),
        Document("readme").owner(User("charlie")),
        Document("readme").viewer(Team("eng").member),
        Document("readme").viewer(
            User("dave").with_ip_range(IpRangeContext(allowed_cidr="10.0.0.0/8"))
        ),
    )

    assert await tc.check(full(), Document("readme").view, User("alice")) is True
    assert await tc.check(full(), Document("readme").edit, User("alice")) is False
    assert await tc.check(full(), Document("readme").view, User("bob")) is True
    assert await tc.check(full(), Document("readme").edit, User("bob")) is True
    assert await tc.check(full(), Document("readme").delete, User("charlie")) is True
    assert await tc.check(full(), Document("readme").view, Team("eng").member) is True


@pytest.mark.integration
async def test_lookup_resources(tc: TypedClient) -> None:
    """Test lookup_resources returns resources a user can access."""
    await tc.touch(Document("readme").viewer(User("alice")))

    ids = [
        r.resource_id
        async for r in tc.lookup_resources(full(), DocumentView, User("alice"))
    ]
    assert "readme" in ids


@pytest.mark.integration
async def test_lookup_subjects(tc: TypedClient) -> None:
    """Test lookup_subjects returns subjects with a permission on a resource."""
    await tc.touch(
        Document("readme").viewer(User("alice")),
        Document("readme").editor(User("bob")),
        Document("readme").owner(User("charlie")),
    )

    ids = [
        s.subject.subject_id
        async for s in tc.lookup_subjects(full(), Document("readme").view, User)
    ]
    assert "alice" in ids
    assert "bob" in ids
    assert "charlie" in ids


@pytest.mark.integration
async def test_read_relationships(tc: TypedClient) -> None:
    """Test read_relationships returns relationships matching a filter."""
    await tc.touch(Document("readme").viewer(User("alice")))

    rels = [r async for r in tc.read_relationships(full(), Filter(resource_type="document"))]
    assert len(rels) > 0


@pytest.mark.integration
async def test_create_and_delete(tc: TypedClient) -> None:
    """Test create shortcut writes new relationships and delete removes them."""
    await tc.create(Document("manual").viewer(User("erin")))
    assert await tc.check(full(), Document("manual").view, User("erin")) is True

    await tc.delete(Document("manual").viewer(User("erin")))
    assert await tc.check(full(), Document("manual").view, User("erin")) is False


@pytest.mark.integration
async def test_typed_transaction_mixed_ops(tc: TypedClient) -> None:
    """TypedTransaction batches mixed create/touch/delete operations atomically."""
    # Seed an existing relationship to delete inside the transaction.
    await tc.touch(Document("rfc").owner(User("eve")))

    txn = TypedTransaction()
    txn.create(Document("rfc").viewer(User("alice")))
    txn.touch(Document("rfc").editor(User("bob")))
    txn.delete(Document("rfc").owner(User("eve")))
    revision = await tc.write(txn)
    assert revision  # non-empty revision token

    assert await tc.check(full(), Document("rfc").view, User("alice")) is True
    assert await tc.check(full(), Document("rfc").edit, User("bob")) is True
    assert await tc.check(full(), Document("rfc").delete, User("eve")) is False


@pytest.mark.integration
async def test_typed_transaction_chainable(tc: TypedClient) -> None:
    """TypedTransaction methods are chainable (return self)."""
    txn = TypedTransaction()
    result = txn.create(Document("doc1").viewer(User("user1"))).touch(
        Document("doc2").editor(User("user2"))
    ).delete(
        Document("doc3").owner(User("user3"))
    )
    assert result is txn


@pytest.mark.integration
async def test_typed_transaction_must_not_match_precondition(tc: TypedClient) -> None:
    """A must_not_match precondition aborts the write if the filter matches anything."""
    # Seed: there IS a viewer on "guarded".
    await tc.touch(Document("guarded").viewer(User("alice")))

    # Try to write while asserting NO viewer relationships exist on "guarded"; must fail.
    txn = TypedTransaction()
    txn.touch(Document("guarded").editor(User("bob")))
    txn.must_not_match(
        Filter(resource_type="document", resource_id="guarded", relation="viewer")
    )

    with pytest.raises(Exception):
        await tc.write(txn)


@pytest.mark.integration
async def test_typed_transaction_must_match_precondition(tc: TypedClient) -> None:
    """A must_match precondition aborts the write if the filter does NOT match anything."""
    # Seed: an owner exists on "protected".
    await tc.touch(Document("protected").owner(User("alice")))

    # This should succeed: owner exists, so must_match passes.
    txn = TypedTransaction()
    txn.touch(Document("protected").viewer(User("bob")))
    txn.must_match(
        Filter(resource_type="document", resource_id="protected", relation="owner")
    )
    revision = await tc.write(txn)
    assert revision

    # Now try with a document that has no owner; must_match should fail.
    txn2 = TypedTransaction()
    txn2.touch(Document("unprotected").viewer(User("charlie")))
    txn2.must_match(
        Filter(resource_type="document", resource_id="unprotected", relation="owner")
    )

    with pytest.raises(Exception):
        await tc.write(txn2)


@pytest.mark.integration
async def test_caveat_time_window(tc: TypedClient) -> None:
    """Test caveated relationships with time_window caveat."""
    await tc.touch(
        Document("restricted").viewer(
            User("frank").with_time_window(
                TimeWindowContext(start="2026-01-01", end="2026-12-31")
            )
        )
    )

    # Relationship exists with caveat context
    rels = [
        r
        async for r in tc.read_relationships(
            full(), Filter(resource_type="document", resource_id="restricted")
        )
    ]
    assert len(rels) > 0


@pytest.mark.integration
async def test_team_member_subject(tc: TypedClient) -> None:
    """Test team#member subject type in relationships and checks."""
    await tc.touch(
        Document("teamdoc").viewer(Team("backend").member),
    )

    assert (
        await tc.check(full(), Document("teamdoc").view, Team("backend").member)
        is True
    )

    # Lookup subjects of type team#member
    team_members = [
        s.subject.subject_id
        async for s in tc.lookup_subjects(
            full(), Document("teamdoc").view, TeamMember
        )
    ]
    # The ID returned is the team ID ("backend"), not the full team#member subject
    assert "backend" in team_members
