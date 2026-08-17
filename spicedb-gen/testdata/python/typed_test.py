"""Integration tests for the generated typed Python client, both flavors.

permissions.py holds the flavor-agnostic types; sync.py and aio.py each
define a TypedClient bound to spicedb.sync/aio.SpiceDBClient respectively.
TestAsyncTypedClient and TestSyncTypedClient exercise the same scenarios
against each flavor, so a change that (for example) silently drops `await`
in the wrong place or leaves an async iterator in the sync client fails
here, not just at import time.

Requires SpiceDB at localhost:50051.
"""

from __future__ import annotations

import pytest
from spicedb import full, Filter

from testdata.permissions import (
    TypedTransaction,
    Document,
    User,
    Team,
    TeamMember,
    IpRangeContext,
    TimeWindowContext,
    DocumentView,
)
from testdata.aio import TypedClient as AsyncTypedClient
from testdata.sync import TypedClient as SyncTypedClient

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


class TestAsyncTypedClient:
    """Exercises aio.TypedClient against a live SpiceDB."""

    @pytest.fixture
    async def tc(self):
        client = AsyncTypedClient.connect(
            "localhost:50051", "somerandomkeyhere", insecure=True
        )
        try:
            await client.client.write_schema(SCHEMA)
            yield client
        finally:
            await client.close()

    @pytest.mark.integration
    async def test_touch_and_check(self, tc: AsyncTypedClient) -> None:
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
    async def test_lookup_resources(self, tc: AsyncTypedClient) -> None:
        """Test lookup_resources returns resources a user can access."""
        await tc.touch(Document("readme").viewer(User("alice")))

        ids = [
            r.resource_id
            async for r in tc.lookup_resources(full(), DocumentView, User("alice"))
        ]
        assert "readme" in ids

    @pytest.mark.integration
    async def test_lookup_subjects(self, tc: AsyncTypedClient) -> None:
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
    async def test_read_relationships(self, tc: AsyncTypedClient) -> None:
        """Test read_relationships returns relationships matching a filter."""
        await tc.touch(Document("readme").viewer(User("alice")))

        rels = [
            r
            async for r in tc.read_relationships(
                full(), Filter(resource_type="document")
            )
        ]
        assert len(rels) > 0

    @pytest.mark.integration
    async def test_create_and_delete(self, tc: AsyncTypedClient) -> None:
        """Test create shortcut writes new relationships and delete removes them."""
        await tc.create(Document("manual").viewer(User("erin")))
        assert await tc.check(full(), Document("manual").view, User("erin")) is True

        await tc.delete(Document("manual").viewer(User("erin")))
        assert await tc.check(full(), Document("manual").view, User("erin")) is False

    @pytest.mark.integration
    async def test_typed_transaction_mixed_ops(self, tc: AsyncTypedClient) -> None:
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
    async def test_typed_transaction_chainable(self, tc: AsyncTypedClient) -> None:
        """TypedTransaction methods are chainable (return self)."""
        txn = TypedTransaction()
        result = txn.create(Document("doc1").viewer(User("user1"))).touch(
            Document("doc2").editor(User("user2"))
        ).delete(
            Document("doc3").owner(User("user3"))
        )
        assert result is txn

    @pytest.mark.integration
    async def test_typed_transaction_must_not_match_precondition(
        self, tc: AsyncTypedClient
    ) -> None:
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
    async def test_typed_transaction_must_match_precondition(
        self, tc: AsyncTypedClient
    ) -> None:
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
    async def test_caveat_time_window(self, tc: AsyncTypedClient) -> None:
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
    async def test_team_member_subject(self, tc: AsyncTypedClient) -> None:
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


class TestSyncTypedClient:
    """Exercises sync.TypedClient against a live SpiceDB.

    Same scenarios as TestAsyncTypedClient, minus await/async — mirrors how
    spicedb/sync/client.py itself drops async from spicedb/aio/client.py.
    """

    @pytest.fixture
    def tc(self):
        client = SyncTypedClient.connect(
            "localhost:50051", "somerandomkeyhere", insecure=True
        )
        try:
            client.client.write_schema(SCHEMA)
            yield client
        finally:
            client.close()

    @pytest.mark.integration
    def test_touch_and_check(self, tc: SyncTypedClient) -> None:
        """Test touch shortcut to write multiple relationships and check permissions."""
        # Resource/team IDs here are suffixed "-sync" (distinct from the -async
        # suite above) since both suites run against the same live SpiceDB
        # instance in one test session; sharing IDs would make a non-idempotent
        # `create` in one suite collide with data left behind by the other.
        tc.touch(
            Document("readme-sync").viewer(User("alice")),
            Document("readme-sync").editor(User("bob")),
            Document("readme-sync").owner(User("charlie")),
            Document("readme-sync").viewer(Team("eng-sync").member),
            Document("readme-sync").viewer(
                User("dave").with_ip_range(IpRangeContext(allowed_cidr="10.0.0.0/8"))
            ),
        )

        assert tc.check(full(), Document("readme-sync").view, User("alice")) is True
        assert tc.check(full(), Document("readme-sync").edit, User("alice")) is False
        assert tc.check(full(), Document("readme-sync").view, User("bob")) is True
        assert tc.check(full(), Document("readme-sync").edit, User("bob")) is True
        assert tc.check(full(), Document("readme-sync").delete, User("charlie")) is True
        assert tc.check(full(), Document("readme-sync").view, Team("eng-sync").member) is True

    @pytest.mark.integration
    def test_lookup_resources(self, tc: SyncTypedClient) -> None:
        """Test lookup_resources returns resources a user can access."""
        tc.touch(Document("readme-sync").viewer(User("alice")))

        ids = [
            r.resource_id
            for r in tc.lookup_resources(full(), DocumentView, User("alice"))
        ]
        assert "readme-sync" in ids

    @pytest.mark.integration
    def test_lookup_subjects(self, tc: SyncTypedClient) -> None:
        """Test lookup_subjects returns subjects with a permission on a resource."""
        tc.touch(
            Document("readme-sync").viewer(User("alice")),
            Document("readme-sync").editor(User("bob")),
            Document("readme-sync").owner(User("charlie")),
        )

        ids = [
            s.subject.subject_id
            for s in tc.lookup_subjects(full(), Document("readme-sync").view, User)
        ]
        assert "alice" in ids
        assert "bob" in ids
        assert "charlie" in ids

    @pytest.mark.integration
    def test_read_relationships(self, tc: SyncTypedClient) -> None:
        """Test read_relationships returns relationships matching a filter."""
        tc.touch(Document("readme-sync").viewer(User("alice")))

        rels = [
            r
            for r in tc.read_relationships(
                full(), Filter(resource_type="document", resource_id="readme-sync")
            )
        ]
        assert len(rels) > 0

    @pytest.mark.integration
    def test_create_and_delete(self, tc: SyncTypedClient) -> None:
        """Test create shortcut writes new relationships and delete removes them."""
        tc.create(Document("manual-sync").viewer(User("erin")))
        assert tc.check(full(), Document("manual-sync").view, User("erin")) is True

        tc.delete(Document("manual-sync").viewer(User("erin")))
        assert tc.check(full(), Document("manual-sync").view, User("erin")) is False

    @pytest.mark.integration
    def test_typed_transaction_mixed_ops(self, tc: SyncTypedClient) -> None:
        """TypedTransaction batches mixed create/touch/delete operations atomically."""
        # Seed an existing relationship to delete inside the transaction.
        tc.touch(Document("rfc-sync").owner(User("eve")))

        txn = TypedTransaction()
        txn.create(Document("rfc-sync").viewer(User("alice")))
        txn.touch(Document("rfc-sync").editor(User("bob")))
        txn.delete(Document("rfc-sync").owner(User("eve")))
        revision = tc.write(txn)
        assert revision  # non-empty revision token

        assert tc.check(full(), Document("rfc-sync").view, User("alice")) is True
        assert tc.check(full(), Document("rfc-sync").edit, User("bob")) is True
        assert tc.check(full(), Document("rfc-sync").delete, User("eve")) is False

    @pytest.mark.integration
    def test_typed_transaction_chainable(self, tc: SyncTypedClient) -> None:
        """TypedTransaction methods are chainable (return self)."""
        txn = TypedTransaction()
        result = txn.create(Document("doc1-sync").viewer(User("user1"))).touch(
            Document("doc2-sync").editor(User("user2"))
        ).delete(
            Document("doc3-sync").owner(User("user3"))
        )
        assert result is txn

    @pytest.mark.integration
    def test_typed_transaction_must_not_match_precondition(
        self, tc: SyncTypedClient
    ) -> None:
        """A must_not_match precondition aborts the write if the filter matches anything."""
        # Seed: there IS a viewer on "guarded-sync".
        tc.touch(Document("guarded-sync").viewer(User("alice")))

        # Try to write while asserting NO viewer relationships exist on "guarded-sync"; must fail.
        txn = TypedTransaction()
        txn.touch(Document("guarded-sync").editor(User("bob")))
        txn.must_not_match(
            Filter(resource_type="document", resource_id="guarded-sync", relation="viewer")
        )

        with pytest.raises(Exception):
            tc.write(txn)

    @pytest.mark.integration
    def test_typed_transaction_must_match_precondition(
        self, tc: SyncTypedClient
    ) -> None:
        """A must_match precondition aborts the write if the filter does NOT match anything."""
        # Seed: an owner exists on "protected-sync".
        tc.touch(Document("protected-sync").owner(User("alice")))

        # This should succeed: owner exists, so must_match passes.
        txn = TypedTransaction()
        txn.touch(Document("protected-sync").viewer(User("bob")))
        txn.must_match(
            Filter(resource_type="document", resource_id="protected-sync", relation="owner")
        )
        revision = tc.write(txn)
        assert revision

        # Now try with a document that has no owner; must_match should fail.
        txn2 = TypedTransaction()
        txn2.touch(Document("unprotected-sync").viewer(User("charlie")))
        txn2.must_match(
            Filter(resource_type="document", resource_id="unprotected-sync", relation="owner")
        )

        with pytest.raises(Exception):
            tc.write(txn2)

    @pytest.mark.integration
    def test_caveat_time_window(self, tc: SyncTypedClient) -> None:
        """Test caveated relationships with time_window caveat."""
        tc.touch(
            Document("restricted-sync").viewer(
                User("frank").with_time_window(
                    TimeWindowContext(start="2026-01-01", end="2026-12-31")
                )
            )
        )

        # Relationship exists with caveat context
        rels = [
            r
            for r in tc.read_relationships(
                full(), Filter(resource_type="document", resource_id="restricted-sync")
            )
        ]
        assert len(rels) > 0

    @pytest.mark.integration
    def test_team_member_subject(self, tc: SyncTypedClient) -> None:
        """Test team#member subject type in relationships and checks."""
        tc.touch(
            Document("teamdoc-sync").viewer(Team("backend-sync").member),
        )

        assert (
            tc.check(full(), Document("teamdoc-sync").view, Team("backend-sync").member)
            is True
        )

        # Lookup subjects of type team#member
        team_members = [
            s.subject.subject_id
            for s in tc.lookup_subjects(full(), Document("teamdoc-sync").view, TeamMember)
        ]
        # The ID returned is the team ID ("backend-sync"), not the full team#member subject
        assert "backend-sync" in team_members
