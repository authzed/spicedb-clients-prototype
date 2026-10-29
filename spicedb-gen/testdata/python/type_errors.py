"""Deliberate type errors. pyright MUST reject every marked line.

The Magefile runs pyright with JSON output and requires an error on each line
marked "# should not type-check", so one failing line cannot mask the others.
"""

from __future__ import annotations

from datetime import datetime

from spicedb import ObjectRef, SubjectRef, Transaction, at_least, full
from spicedb.testing import AsyncInMemoryClient, InMemoryClient

from testdata.permissions import (
    DOCUMENT_VIEW,
    Document,
    Invitation,
    IpRangeContext,
    Organization,
    Team,
    TeamMember,
    TimeWindowContext,
    TypedTransaction,
    User,
    UserWildcard,
    definition_by_name,
    object_by_name,
    parse_object,
    permission_ref_by_name,
    relationship_from_triple,
    subject_set_by_name,
)
from testdata.aio import TypedClient as AsyncTypedClient
from testdata.sync import TypedClient

# Team (the resource) is not a document viewer; only the TeamMember userset is.
Document("readme").viewer(Team("eng"))  # should not type-check
# TeamMember is not a DocumentEditorSubject (= User).
Document("readme").editor(TeamMember("eng"))  # should not type-check
# Wrong caveat-context type for with_ip_range.
User("alice").with_ip_range(TimeWindowContext(start="x", end="y"))  # should not type-check
# Wrong field type on IpRangeContext.
IpRangeContext(allowed_cidr=42)  # should not type-check
# editor does not admit a caveated user.
Document("x").editor(User("x").with_ip_range(IpRangeContext()))  # should not type-check

# grant admits only organization#can_admin, a permission userset.
Invitation("x").grant(User("alice"))  # should not type-check
Invitation("x").grant(Organization("acme"))  # should not type-check
# can_admin stays the permission accessor; the subject is can_admin_subject.
Invitation("x").grant(Organization("acme").can_admin)  # should not type-check
# timed_grant admits only the expiring userset.
Invitation("x").timed_grant(Organization("acme").can_admin_subject)  # should not type-check

# creator admits only user:*, never a concrete user; guest admits both.
Organization("x").creator(User("alice"))  # should not type-check
Organization("x").creator_key(User("alice"))  # should not type-check
Organization("x").admin(UserWildcard())  # should not type-check

# inviter admits only expiring users; its key admits only the base user.
Invitation("x").inviter(User("alice"))  # should not type-check
Invitation("x").inviter_key(User("alice").with_expiration(datetime.now()))  # should not type-check
# Plain relations have no key: their writer already identifies the tuple.
Document("x").editor_key(User("alice"))  # should not type-check
# Keys delete; they never write.
TypedTransaction().touch(Invitation("x").inviter_key(User("alice")))  # should not type-check

# Native token and named-mode contracts also apply to generated clients.
at_least("plain revision")  # should not type-check
TypedClient.connect("remote.example:50051", "secret", remote_credentials=True)  # should not type-check


def invalid_mode(client: TypedClient) -> None:
    client.check(full(), Document("readme").view, User("alice"), tracing=True)  # should not type-check
    client.lookup_resources(full(), DOCUMENT_VIEW, User("alice"), permissionship_filter=True)  # should not type-check
    at_least(client.touch(Document("readme").viewer(User("alice"))))  # should not type-check

    # Schema-aware overloads stay exact.
    client.check(full(), Document("one").view, Team("eng"))  # should not type-check
    client.check(full(), Organization("acme").member_relation, Team("eng"))  # should not type-check
    client.lookup_resources(full(), DOCUMENT_VIEW, Team("eng"), page_size=2)  # should not type-check
    client.lookup_subjects(full(), Document("one").view, Team, limit=2)  # should not type-check
    client.watch(filters=["document"])  # should not type-check
    client.delete_relationships("document", limit=2)  # should not type-check

    # write_txn does not shadow the native write: write takes only a native Transaction.
    client.write(TypedTransaction())  # should not type-check
    client.write_txn(Transaction())  # should not type-check

    # Runtime-name lookups take names and a generated subject.
    client.lookup_resources_by_name(full(), "document", "view", SubjectRef(ObjectRef("user", "a")))  # should not type-check
    client.lookup_resources_by_name(full(), Document, "view", User("alice"))  # should not type-check
    client.lookup_subjects_by_name(full(), "document", "readme", "view", User)  # should not type-check
    client.lookup_subjects_by_name(full(), Document("readme"), "view", "user")  # should not type-check


# The typed client wraps a client of its own flavor only.
TypedClient(AsyncInMemoryClient())  # should not type-check
AsyncTypedClient(InMemoryClient())  # should not type-check
# Schema-level lookups take names.
definition_by_name(Document)  # should not type-check
permission_ref_by_name("document", DOCUMENT_VIEW)  # should not type-check
object_by_name("document", 7)  # should not type-check
parse_object(ObjectRef("document", "readme"))  # should not type-check
relationship_from_triple("document:readme", "viewer", User("alice"))  # should not type-check
# A typed object from a runtime name is the union of definitions, not one of them.
_doc_only: Document = object_by_name("document", "readme")  # should not type-check
# The result is a relationship, not a transaction.
relationship_from_triple("document:readme", "viewer", "user:alice").build()  # should not type-check
subject_set_by_name("team", "eng")  # should not type-check
# A subject set is a userset, not a plain object.
_team_only: Team = subject_set_by_name("team", "eng", "member")  # should not type-check
