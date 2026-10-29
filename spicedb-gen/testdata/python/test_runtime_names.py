"""Generated schema-level lookups, name constants and schema text, and the
typed client wrapping the spicedb.testing doubles. No server is needed.

Code that receives type and permission names at runtime (a policy layer, an
admin tool) stays typed: it resolves names against the schema, and the typed
client it uses in tests wraps the in-memory double.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from datetime import datetime, timezone

from spicedb import (
    Caveat,
    InvalidArgumentError,
    LookupPermissionshipFilter,
    ObjectRef,
    Operation,
    Relationship,
    Permissionship,
    SubjectRef,
    full,
)
from spicedb.protocols import AsyncClientProtocol, ClientProtocol
from spicedb.testing import AsyncInMemoryClient, InMemoryClient, grant_all

from testdata.aio import TypedClient as AsyncTypedClient
from testdata.permissions import (
    DEFINITION_NAMES,
    DOCUMENT_VIEW,
    SCHEMA_TEXT,
    DefinitionNames,
    Document,
    NotInSchemaError,
    Organization,
    Invitation,
    OrganizationCanAdmin,
    PermissionRef,
    SchemaObject,
    SubjectSet,
    SubjectNotAllowedError,
    Team,
    TeamMember,
    User,
    UserWildcard,
    TypedTransaction,
    definition_by_name,
    object_by_name,
    parse_object,
    permission_ref_by_name,
    relationship_from_triple,
    subject_set_by_name,
)
from testdata.sync import TypedClient

SAMPLE = Path(__file__).resolve().parents[1] / "sample.zed"


def test_schema_text_and_definition_names():
    assert SCHEMA_TEXT == SAMPLE.read_text()
    assert DEFINITION_NAMES == ("user", "team", "document", "organization", "invitation")
    assert DefinitionNames.DOCUMENT == "document"
    assert DefinitionNames.INVITATION == "invitation"


def test_definition_by_name():
    assert definition_by_name("document") is Document
    doc = definition_by_name("document")("readme")
    assert doc == Document("readme")
    assert doc.permission_by_name("view") == Document("readme").view
    for name in DEFINITION_NAMES:
        assert definition_by_name(name)("x").__class__.__name__.lower() == name.replace("_", "")
    with pytest.raises(NotInSchemaError, match="no definition 'folder'"):
        definition_by_name("folder")


def test_permission_ref_by_name():
    ref = permission_ref_by_name("document", "view")
    assert ref is DOCUMENT_VIEW
    assert isinstance(ref, PermissionRef)
    assert (ref.resource_type, ref.name, str(ref)) == ("document", "view", "document#view")
    with pytest.raises(NotInSchemaError, match="no definition"):
        permission_ref_by_name("folder", "view")
    with pytest.raises(NotInSchemaError, match="no permission 'viewer'"):
        permission_ref_by_name("document", "viewer")  # a relation, not a permission


def test_typed_client_wraps_the_double():
    double = InMemoryClient()
    client = TypedClient(double)
    native: ClientProtocol = client.client
    assert native is double

    client.touch(Document("readme").viewer(User("alice")), Document("public").viewer(User("bob")))
    # The double resolves stored relationships whose relation is the checked name.
    assert client.check(full(), Document("readme").viewer_relation, User("alice")).has_permission
    assert not client.check(full(), Document("readme").viewer_relation, User("bob")).has_permission
    # It evaluates no schema: a permission needs a registered result or a resolver.
    assert not client.check(full(), Document("readme").view, User("alice")).has_permission
    double.set_check_resolver(lambda c: c.permission == "view" and c.subject.object.object_id == "alice" or None)
    assert client.check(full(), Document("readme").view, User("alice")).has_permission

    found = [r.resource_id for r in client.lookup_resources(full(), DOCUMENT_VIEW, User("alice"))]
    assert found == ["readme", "public"]
    subjects = [s.subject.subject_id for s in client.lookup_subjects(full(), Document("readme").view, User)]
    assert subjects == ["alice"]
    assert [c.operation for c in double.calls()][-2:] == [Operation.LOOKUP_RESOURCES, Operation.LOOKUP_SUBJECTS]


def test_lookup_resources_by_name_validates_before_any_call():
    double = InMemoryClient()
    double.set_check_resolver(grant_all)
    client = TypedClient(double)
    client.touch(Organization("acme").member(User("alice")))

    found = client.lookup_resources_by_name(
        full(), "organization", "view", User("alice"),
        permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY, limit=5,
    )
    assert [(r.resource_id, r.permissionship) for r in found] == [("acme", Permissionship.HAS_PERMISSION)]
    call = double.calls_of(Operation.LOOKUP_RESOURCES)[0]
    assert call.options["resource_type"] == "organization" and call.options["permission"] == "view"
    assert call.options["subject"] == SubjectRef(ObjectRef("user", "alice"))
    assert call.options["limit"] == 5

    before = len(double.calls())
    with pytest.raises(NotInSchemaError):
        client.lookup_resources_by_name(full(), "folder", "view", User("alice"))
    with pytest.raises(NotInSchemaError, match="no permission or relation 'viewers'"):
        client.lookup_resources_by_name(full(), "document", "viewers", User("alice"))
    # A subject type that cannot reach the permission has no results, as
    # SpiceDB would answer, and no call is made.
    assert list(client.lookup_resources_by_name(full(), "document", "view", Organization("acme"))) == []
    assert list(client.lookup_resources_by_name(full(), "organization", "admin", Team("eng").member)) == []
    # A reachable type in a shape the schema does not admit is still an error.
    with pytest.raises(SubjectNotAllowedError):
        client.lookup_resources_by_name(full(), "organization", "view", UserWildcard())
    assert len(double.calls()) == before, "answered before any call"


def test_lookup_resources_by_name_accepts_relations():
    double = InMemoryClient()
    client = TypedClient(double)
    client.touch(Organization("acme").member(User("alice")))
    found = [r.resource_id for r in client.lookup_resources_by_name(full(), "organization", "member", User("alice"))]
    assert found == ["acme"]
    call = double.calls_of(Operation.LOOKUP_RESOURCES)[-1]
    assert (call.options["resource_type"], call.options["permission"]) == ("organization", "member")


def test_lookup_subjects_by_name():
    double = InMemoryClient()
    double.set_check_resolver(grant_all)
    client = TypedClient(double)
    client.touch(Document("readme").viewer(User("alice")), Team("eng").for_member(User("bob")))

    found = [s.subject.subject_id for s in client.lookup_subjects_by_name(full(), "document", "readme", "view", "user")]
    assert found == ["alice", "bob"]
    call = double.calls_of(Operation.LOOKUP_SUBJECTS)[-1]
    assert call.options["resource"] == ObjectRef("document", "readme")
    assert (call.options["permission"], call.options["subject_type"]) == ("view", "user")
    assert call.options["subject_relation"] is None
    # Relations are looked up as well as permissions.
    list(client.lookup_subjects_by_name(full(), "document", "readme", "viewer", "team", "member", limit=3))
    call = double.calls_of(Operation.LOOKUP_SUBJECTS)[-1]
    assert (call.options["permission"], call.options["subject_relation"], call.options["limit"]) == (
        "viewer", "member", 3,
    )

    before = len(double.calls())
    for args in (
        ("folder", "readme", "view", "user"),
        ("document", "readme", "viewers", "user"),
        ("document", "readme", "view", "robot"),
        ("document", "readme", "view", "team", "members"),
    ):
        with pytest.raises(NotInSchemaError):
            client.lookup_subjects_by_name(full(), *args)
    with pytest.raises(ValueError):
        client.lookup_subjects_by_name(full(), "document", "has space", "view", "user")
    # Unreachable subject types have no results, without a call.
    assert list(client.lookup_subjects_by_name(full(), "document", "readme", "view", "organization")) == []
    assert list(client.lookup_subjects_by_name(full(), "document", "readme", "view", "team")) == []
    assert len(double.calls()) == before


async def test_async_by_name_lookups_of_unreachable_subjects_are_empty():
    double = AsyncInMemoryClient()
    client = AsyncTypedClient(double)
    found = [r async for r in client.lookup_resources_by_name(full(), "document", "view", Organization("acme"))]
    assert found == []
    subjects = [s async for s in client.lookup_subjects_by_name(full(), "document", "readme", "view", "organization")]
    assert subjects == []
    assert double.calls() == []
    async with client.lookup_subjects_by_name(full(), "document", "readme", "view", "user") as stream:
        assert [s async for s in stream] == []
    assert [c.operation for c in double.calls()] == [Operation.LOOKUP_SUBJECTS]


def test_subject_set_by_name():
    subject = subject_set_by_name("team", "eng", "member")
    assert subject == TeamMember("eng") == Team("eng").member
    assert str(subject) == "team:eng#member"
    assert subject_set_by_name("organization", "acme", "can_admin") == Organization("acme").can_admin_subject
    with pytest.raises(NotInSchemaError, match="no definition"):
        subject_set_by_name("group", "g1", "member")
    with pytest.raises(NotInSchemaError, match="not a subject set"):
        subject_set_by_name("document", "readme", "viewer")  # a relation no relation admits as a subject
    with pytest.raises(NotInSchemaError, match="not a subject set"):
        subject_set_by_name("team", "eng", "owner")
    with pytest.raises(ValueError):
        subject_set_by_name("team", "", "member")
    # A by-name subject set is a typed subject the typed API accepts.
    rel = Document("readme").relationship_by_name("viewer", subject_set_by_name("team", "eng", "member"))
    assert str(rel) == "document:readme#viewer@team:eng#member"


def test_schema_object_covers_every_definition():
    objects: list[SchemaObject] = [parse_object(f"{name}:x") for name in DEFINITION_NAMES]
    assert [type(o) for o in objects] == [User, Team, Document, Organization, Invitation]
    sets: list[SubjectSet] = [TeamMember("eng"), OrganizationCanAdmin("acme")]
    assert len(sets) == 2


async def test_async_typed_client_wraps_the_async_double():
    double = AsyncInMemoryClient()
    client = AsyncTypedClient(double)
    native: AsyncClientProtocol = client.client
    assert native is double
    await client.touch(Team("eng").for_member(User("alice")))
    assert (await client.check(full(), Team("eng").member_relation, User("alice"))).has_permission
    double.set_check_resolver(grant_all)
    names = [r.resource_id async for r in client.lookup_resources_by_name(full(), "document", "view", User("alice"))]
    assert names == []  # no document is known to the double
    await client.touch(Document("readme").owner(User("alice")))
    names = [r.resource_id async for r in client.lookup_resources_by_name(full(), "document", "view", User("alice"))]
    assert names == ["readme"]


def test_object_by_name_returns_the_typed_ref():
    doc = object_by_name("document", "readme")
    assert doc == Document("readme")
    assert isinstance(doc, Document)
    assert object_by_name("user", "*") == User("*")
    with pytest.raises(NotInSchemaError, match="no definition 'folder'"):
        object_by_name("folder", "readme")
    for bad_id in ("", "has space", "a:b"):
        with pytest.raises(ValueError):  # LocalValidationError, also InvalidArgumentError
            object_by_name("document", bad_id)
        with pytest.raises(InvalidArgumentError):
            object_by_name("document", bad_id)


def test_parse_object():
    assert parse_object("organization:acme") == Organization("acme")
    assert parse_object("document:readme").permission_by_name("view") == Document("readme").view
    with pytest.raises(NotInSchemaError):
        parse_object("folder:readme")
    for bad in ("document", "document:", ":readme", "Document:readme"):
        with pytest.raises(ValueError):
            parse_object(bad)


def test_relationship_from_triple_is_schema_checked():
    r = relationship_from_triple("document:readme", "viewer", "user:alice")
    assert r.to_relationship() == Document("readme").viewer(User("alice")).to_relationship()
    assert relationship_from_triple("document:readme", "viewer", "team:eng#member").to_relationship() == (
        Document("readme").viewer(Team("eng").member).to_relationship()
    )
    assert relationship_from_triple("document:readme", "viewer", "user:alice#...").to_relationship() == (
        Relationship.from_triple("document:readme", "viewer", "user:alice")
    )
    assert str(relationship_from_triple("organization:acme", "creator", "user:*")) == (
        "organization:acme#creator@user:*"
    )
    caveated = relationship_from_triple(
        "document:readme", "viewer", "user:alice", caveat=Caveat("ip_range", {"allowed_cidr": "10.0.0.0/8"})
    )
    assert caveated.to_relationship().caveat == Caveat("ip_range", {"allowed_cidr": "10.0.0.0/8"})
    when = datetime(2030, 1, 1, tzinfo=timezone.utc)
    expiring = relationship_from_triple("invitation:i1", "inviter", "user:alice", expiration=when)
    assert expiring.to_relationship().expiration == when
    # A typed relationship goes straight into a typed transaction.
    txn = TypedTransaction().touch(r).build()
    assert [str(u.relationship) for u in txn.updates] == ["document:readme#viewer@user:alice"]


def test_relationship_from_triple_rejects_what_the_schema_rejects():
    with pytest.raises(NotInSchemaError, match="no definition 'folder'"):
        relationship_from_triple("folder:a", "viewer", "user:alice")
    with pytest.raises(NotInSchemaError, match="no definition 'robot'"):
        relationship_from_triple("document:a", "viewer", "robot:r2")
    with pytest.raises(NotInSchemaError, match="no relation 'view'"):
        relationship_from_triple("document:a", "view", "user:alice")  # a permission
    with pytest.raises(SubjectNotAllowedError):
        relationship_from_triple("document:a", "editor", "team:eng#member")
    with pytest.raises(SubjectNotAllowedError):
        relationship_from_triple("document:a", "viewer", "user:*")  # no wildcard on viewer
    with pytest.raises(SubjectNotAllowedError):
        relationship_from_triple("organization:a", "creator", "user:alice")  # wildcard only
    with pytest.raises(SubjectNotAllowedError):
        relationship_from_triple("invitation:i", "inviter", "user:alice")  # needs an expiration
    with pytest.raises(SubjectNotAllowedError):
        relationship_from_triple("document:a", "editor", "user:alice", caveat=Caveat("ip_range"))
    with pytest.raises(ValueError):
        relationship_from_triple("document", "viewer", "user:alice")


def test_check_by_name_and_check_bulk_by_name_against_the_double():
    double = InMemoryClient()
    client = TypedClient(double)
    client.touch(relationship_from_triple("document:readme", "viewer", "user:alice"))
    assert client.check_by_name(full(), "document", "readme", "viewer", parse_object("user:alice")).has_permission
    assert not client.check_by_name(full(), "document", "readme", "viewer", parse_object("user:bob")).has_permission
    # What definition_by_name builds is accepted by check_dynamic as well.
    perm = definition_by_name("document")("readme").permission_by_name("view")
    double.set_check_resolver(lambda c: c.permission == "view" or None)
    assert client.check_dynamic(full(), perm, parse_object("user:bob")).has_permission

    results = client.check_bulk_by_name(
        full(),
        [
            ("document", "readme", "viewer", parse_object("user:alice")),
            ("document", "readme", "editor", parse_object("user:alice")),
            ("document", "readme", "view", subject_set_by_name("team", "eng", "member")),
        ],
    )
    assert [r.has_permission for r in results] == [True, False, True]
    sent = double.calls_of(Operation.CHECK_BULK)[-1].request
    assert [(c.resource.object_id, c.permission, str(c.subject)) for c in sent] == [
        ("readme", "viewer", "user:alice"),
        ("readme", "editor", "user:alice"),
        ("readme", "view", "team:eng#member"),
    ]

    before = len(double.calls())
    with pytest.raises(NotInSchemaError):
        client.check_by_name(full(), "folder", "readme", "view", User("alice"))
    with pytest.raises(NotInSchemaError):
        client.check_by_name(full(), "document", "readme", "viewers", User("alice"))
    with pytest.raises(SubjectNotAllowedError):
        client.check_by_name(full(), "document", "readme", "view", Organization("acme"))
    with pytest.raises(ValueError):
        client.check_by_name(full(), "document", "", "view", User("alice"))
    with pytest.raises(NotInSchemaError):  # one bad item fails the batch before any call
        client.check_bulk_by_name(
            full(),
            [("document", "readme", "view", User("alice")), ("document", "readme", "nope", User("alice"))],
        )
    assert len(double.calls()) == before


async def test_async_check_by_name():
    double = AsyncInMemoryClient()
    client = AsyncTypedClient(double)
    await client.touch(relationship_from_triple("team:eng", "member", "user:alice"))
    assert (await client.check_by_name(full(), "team", "eng", "member", User("alice"))).has_permission
    results = await client.check_bulk_by_name(full(), [("team", "eng", "member", User("bob"))])
    assert [r.has_permission for r in results] == [False]


def test_typed_client_ready_forwards_to_the_double():
    """ready() is the startup readiness check; the double is always ready."""
    double = InMemoryClient()
    client = TypedClient(double)
    client.ready(timeout=1.0)
    client.ready()
    assert double.calls() == []
    with pytest.raises(InvalidArgumentError, match="timeout"):
        client.ready(timeout=-1)


async def test_async_typed_client_ready_forwards_to_the_double():
    client = AsyncTypedClient(AsyncInMemoryClient())
    await client.ready(timeout=1.0)
    with pytest.raises(InvalidArgumentError, match="timeout"):
        await client.ready(timeout=0)
