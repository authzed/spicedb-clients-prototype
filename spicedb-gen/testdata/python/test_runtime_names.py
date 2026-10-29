"""Generated schema-level lookups, name constants and schema text, and the
typed client wrapping the spicedb.testing doubles. No server is needed.

Code that receives type and permission names at runtime (a policy layer, an
admin tool) stays typed: it resolves names against the schema, and the typed
client it uses in tests wraps the in-memory double.
"""

from __future__ import annotations

from pathlib import Path

import pytest
from spicedb import (
    LookupPermissionshipFilter,
    ObjectRef,
    Operation,
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
    PermissionRef,
    SubjectNotAllowedError,
    Team,
    User,
    UserWildcard,
    definition_by_name,
    permission_ref_by_name,
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
    with pytest.raises(NotInSchemaError):
        client.lookup_resources_by_name(full(), "organization", "member", User("alice"))
    with pytest.raises(SubjectNotAllowedError):
        client.lookup_resources_by_name(full(), "document", "view", Organization("acme"))
    with pytest.raises(SubjectNotAllowedError):
        client.lookup_resources_by_name(full(), "organization", "view", UserWildcard())
    assert len(double.calls()) == before, "rejected before any call"


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
