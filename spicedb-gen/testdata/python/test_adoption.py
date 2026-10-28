"""Typed-client capabilities found missing while migrating real services:
permission usersets, wildcard refs, identity-only keys, relation checks, typed
transactions, filters, bulk checks, native round trips and by-name lookups.

The first group needs no server. The Test*Adoption classes run the same
scenario against a live SpiceDB (SPICEDB_ENDPOINT, default localhost:50051)
with the sync and the asyncio client.
"""

from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest
from spicedb import (
    FailedPreconditionError,
    Filter,
    InvalidArgumentError,
    ObjectRef,
    PermissionCheck,
    Relationship,
    SubjectFilter,
    SubjectRef,
    SubjectRelationFilter,
    TransportSecurity,
    full,
)

from testdata.aio import TypedClient as AsyncTypedClient
from testdata.permissions import (
    DOCUMENT_VIEW,
    INVITATION_PERMISSION_NAMES,
    ORGANIZATION_PERMISSION_NAMES,
    ORGANIZATION_RELATION_NAMES,
    Document,
    Invitation,
    IpRangeContext,
    NotInSchemaError,
    Organization,
    OrganizationCanAdmin,
    OrganizationPermissions,
    OrganizationRelations,
    Permission,
    RelationshipKey,
    SubjectNotAllowedError,
    TypedRelationship,
    TypedTransaction,
    User,
    UserWildcard,
    checks_for,
    with_subject,
)
from testdata.sync import TypedClient as SyncTypedClient

SCHEMA = (Path(__file__).resolve().parents[1] / "sample.zed").read_text()
ENDPOINT = os.environ.get("SPICEDB_ENDPOINT", "localhost:50051")
TOKEN = os.environ.get("SPICEDB_TOKEN", "somerandomkeyhere")


# --- No server ---


def test_permissions_are_observable() -> None:
    p = Organization("acme").can_admin
    assert (p.resource_type, p.resource_id, p.name) == ("organization", "acme", "can_admin")
    assert str(p) == "organization:acme#can_admin"
    assert str(p.ref) == "organization#can_admin"
    assert p.ref == Organization("other").can_admin.ref
    assert str(DOCUMENT_VIEW) == "document#view"
    assert (DOCUMENT_VIEW.resource_type, DOCUMENT_VIEW.name) == ("document", "view")
    assert str(Organization("acme").member_relation) == "organization:acme#member"


def test_subjects_have_string_forms() -> None:
    assert str(User("alice")) == "user:alice"
    assert str(UserWildcard()) == "user:*"
    assert str(Organization("acme").can_admin_subject) == "organization:acme#can_admin"
    expiring = Organization("acme").can_admin_subject.with_expiration(datetime(2030, 1, 1, tzinfo=timezone.utc))
    assert str(expiring) == "organization:acme#can_admin"


def test_typed_relationships_round_trip() -> None:
    typed = Invitation("inv").grant(Organization("acme").can_admin_subject)
    assert str(typed) == "invitation:inv#grant@organization:acme#can_admin"
    native = typed.to_relationship()
    assert native == Relationship(
        ObjectRef("invitation", "inv"), "grant", SubjectRef(ObjectRef("organization", "acme"), "can_admin")
    )
    assert TypedRelationship.from_relationship(native) == typed
    assert str(TypedRelationship.from_relationship(native)) == str(typed)

    caveated = Document("d").viewer(User("bob").with_ip_range(IpRangeContext(allowed_cidr="10.0.0.0/8")))
    assert str(caveated) == 'document:d#viewer@user:bob[ip_range:{"allowed_cidr":"10.0.0.0/8"}]'

    broken = TypedRelationship.from_error(LookupError("no such user"))
    assert "no such user" in str(broken)
    assert isinstance(broken.error, LookupError)
    with pytest.raises(LookupError):
        broken.to_relationship()
    zero = TypedRelationship.from_relationship(Relationship(ObjectRef("", ""), "", SubjectRef(ObjectRef("", ""))))
    assert isinstance(zero.error, InvalidArgumentError), "an empty native relationship is invalid"


def test_writers_defer_construction_errors() -> None:
    bad = Organization("").admin(User("alice"))
    assert isinstance(bad.error, InvalidArgumentError)
    assert "invalid relationship" in str(bad)


def test_transaction_defers_first_error() -> None:
    txn = (
        TypedTransaction()
        .touch(Organization("").admin(User("alice")))  # empty ID: deferred error
        .touch(Organization("acme").admin(User("bob")))
    )
    assert isinstance(txn.error, InvalidArgumentError)
    assert len(txn) == 0, "operations after the first error are ignored"
    with pytest.raises(InvalidArgumentError):
        txn.build()

    ok = (
        TypedTransaction()
        .must_not_match(Organization("acme").admin_filter())
        .delete(Organization("acme").member(User("alice")))
        .touch(Organization("acme").admin(User("alice")))
    )
    assert len(ok) == 2
    assert str(ok) == (
        "[MUST_NOT_MATCH(organization:acme#admin@*), "
        "DELETE(organization:acme#member@user:alice), "
        "TOUCH(organization:acme#admin@user:alice)]"
    )
    first, second = ok.build(), ok.build()
    assert first is not second, "build returns an independent native transaction"


def test_typed_filters_and_name_constants() -> None:
    assert Organization.type_filter() == Filter("organization")
    assert Organization("acme").filter() == Filter("organization", resource_id="acme")
    assert Organization("acme").admin_filter() == Filter("organization", resource_id="acme", relation="admin")
    assert with_subject(Organization("acme").admin_filter(), User("alice")) == Filter(
        "organization",
        resource_id="acme",
        relation="admin",
        subject=SubjectFilter("user", subject_id="alice", relation=SubjectRelationFilter.direct()),
    )
    assert with_subject(Invitation.type_filter(), OrganizationCanAdmin) == Filter(
        "invitation",
        subject=SubjectFilter("organization", relation=SubjectRelationFilter.named("can_admin")),
    )
    with pytest.raises(InvalidArgumentError):
        Organization("").filter()  # an empty ID must not widen to the whole type
    key = Invitation("inv").inviter_key(User("alice"))
    assert isinstance(key, RelationshipKey)
    assert key.filter() == Filter(
        "invitation",
        resource_id="inv",
        relation="inviter",
        subject=SubjectFilter("user", subject_id="alice", relation=SubjectRelationFilter.direct()),
    )
    assert OrganizationRelations.ADMIN == "admin"
    assert OrganizationPermissions.CAN_ADMIN == "can_admin"


def test_by_name_lookups() -> None:
    org = Organization("acme")
    assert org.permission_by_name("can_admin") == org.can_admin
    with pytest.raises(NotInSchemaError):
        org.permission_by_name("admin")  # a relation, not a permission
    assert org.relation_by_name("member") == org.member_relation
    with pytest.raises(NotInSchemaError):
        org.relation_by_name("view")  # a permission, not a relation

    typed = org.relationship_by_name("creator", UserWildcard())
    assert str(typed) == "organization:acme#creator@user:*"
    with pytest.raises(SubjectNotAllowedError):
        org.relationship_by_name("creator", User("alice"))
    with pytest.raises(NotInSchemaError):
        org.relationship_by_name("nope", User("alice"))

    assert ORGANIZATION_PERMISSION_NAMES == ("can_admin", "view")
    assert ORGANIZATION_RELATION_NAMES == ("admin", "member", "creator", "guest")
    assert INVITATION_PERMISSION_NAMES == ("accept",)


def test_permission_checks_are_native_and_validated() -> None:
    check = Document("d").view.permission_check(User("bob").with_ip_range(IpRangeContext(allowed_cidr="x")))
    assert check == PermissionCheck(
        ObjectRef("document", "d"), "view", SubjectRef(ObjectRef("user", "bob")), {"allowed_cidr": "x"}
    )
    by_name: Permission = Organization("acme").permission_by_name("view")
    with pytest.raises(SubjectNotAllowedError):
        by_name.permission_check(Organization("other"))
    assert [c.permission for c in checks_for(User("bob"), Document("d").view, Organization("o").member_relation)] == [
        "view",
        "member",
    ]


# --- Live SpiceDB ---

ACME, INV = Organization("acme-adoption"), Invitation("inv-adoption")


def adoption_txn() -> TypedTransaction:
    later = datetime.now(timezone.utc) + timedelta(hours=1)
    return (
        TypedTransaction()
        .touch(ACME.admin(User("alice")))
        .touch(ACME.member(User("bob")))
        .touch(ACME.creator(UserWildcard()))
        .touch(ACME.guest(UserWildcard()))
        .touch(INV.org(ACME))
        .touch(INV.grant(ACME.can_admin_subject))
        .touch(INV.timed_grant(ACME.can_admin_subject.with_expiration(later)))
        .touch(INV.inviter(User("alice").with_expiration(later)))
    )


def guard_txn(member: str) -> TypedTransaction:
    return (
        TypedTransaction()
        .must_not_match(with_subject(ACME.admin_filter(), User("bob")))
        .touch(ACME.member(User(member)))
    )


@pytest.mark.integration
class TestSyncAdoption:
    @pytest.fixture
    def tc(self):
        client = SyncTypedClient.connect(ENDPOINT, TOKEN, transport_security=TransportSecurity.PLAINTEXT)
        try:
            client.write_schema(SCHEMA)
            for f in (ACME.filter(), INV.filter()):
                client.delete_all_relationships_non_atomic(f)
            yield client
        finally:
            client.close()

    def test_adoption(self, tc: SyncTypedClient) -> None:
        cs = full()
        # G1 + T6: permission userset and wildcard subjects write and check.
        tc.write_txn(adoption_txn())
        assert tc.check(cs, INV.accept, User("alice")).has_permission, "alice accepts through can_admin"
        assert tc.check(cs, ACME.view, User("anyone")).has_permission, "user:* guest grants view"

        # T8: relation checks.
        assert tc.check(cs, ACME.member_relation, User("bob")).has_permission
        assert not tc.check(cs, ACME.member_relation, User("alice")).has_permission

        # T4: typed bulk check through the native check_bulk, in input order.
        results = tc.check_bulk(checks_for(User("bob"), ACME.can_admin, ACME.view, ACME.member_relation), consistency=cs)
        assert [r.has_permission for r in results] == [False, True, True]

        # T2: a precondition guards a mixed delete+touch; a failing guard writes nothing.
        tc.write_txn(
            TypedTransaction()
            .must_not_match(with_subject(ACME.admin_filter(), User("bob")))
            .delete(ACME.member(User("bob")))
            .touch(ACME.admin(User("bob")))
        )
        with pytest.raises(FailedPreconditionError):
            tc.write_txn(guard_txn("carol"))
        assert not tc.check(cs, ACME.member_relation, User("carol")).has_permission

        # T3 + T5: read back with a typed filter and delete what was read.
        txn, admins = TypedTransaction(), []
        for item in tc.read_relationships(cs, ACME.admin_filter()):
            admins.append(item.relationship.subject.object.object_id)
            txn.delete(TypedRelationship.from_relationship(item.relationship))
        assert sorted(admins) == ["alice", "bob"]
        tc.write_txn(txn)
        assert list(tc.read_relationships(cs, ACME.admin_filter())) == []

        # T7: delete an expiration-only relationship by identity alone.
        tc.delete(INV.inviter_key(User("alice")))
        assert list(tc.read_relationships(cs, INV.inviter_filter())) == []

        # T9: by-name lookups drive real checks.
        assert tc.check_dynamic(cs, ACME.permission_by_name("view"), User("anyone")).has_permission


@pytest.mark.integration
class TestAsyncAdoption:
    @pytest.fixture
    async def tc(self):
        client = AsyncTypedClient.connect(ENDPOINT, TOKEN, transport_security=TransportSecurity.PLAINTEXT)
        try:
            await client.write_schema(SCHEMA)
            for f in (ACME.filter(), INV.filter()):
                await client.delete_all_relationships_non_atomic(f)
            yield client
        finally:
            await client.close()

    async def test_adoption(self, tc: AsyncTypedClient) -> None:
        cs = full()
        await tc.write_txn(adoption_txn())
        assert (await tc.check(cs, INV.accept, User("alice"))).has_permission
        assert (await tc.check(cs, ACME.view, User("anyone"))).has_permission

        assert (await tc.check(cs, ACME.member_relation, User("bob"))).has_permission
        assert not (await tc.check(cs, ACME.member_relation, User("alice"))).has_permission

        results = await tc.check_bulk(
            checks_for(User("bob"), ACME.can_admin, ACME.view, ACME.member_relation), consistency=cs
        )
        assert [r.has_permission for r in results] == [False, True, True]

        await tc.write_txn(
            TypedTransaction()
            .must_not_match(with_subject(ACME.admin_filter(), User("bob")))
            .delete(ACME.member(User("bob")))
            .touch(ACME.admin(User("bob")))
        )
        with pytest.raises(FailedPreconditionError):
            await tc.write_txn(guard_txn("carol"))
        assert not (await tc.check(cs, ACME.member_relation, User("carol"))).has_permission

        txn, admins = TypedTransaction(), []
        async for item in tc.read_relationships(cs, ACME.admin_filter()):
            admins.append(item.relationship.subject.object.object_id)
            txn.delete(TypedRelationship.from_relationship(item.relationship))
        assert sorted(admins) == ["alice", "bob"]
        await tc.write_txn(txn)
        assert [r async for r in tc.read_relationships(cs, ACME.admin_filter())] == []

        await tc.delete(INV.inviter_key(User("alice")))
        assert [r async for r in tc.read_relationships(cs, INV.inviter_filter())] == []

        assert (await tc.check_dynamic(cs, ACME.permission_by_name("view"), User("anyone"))).has_permission
