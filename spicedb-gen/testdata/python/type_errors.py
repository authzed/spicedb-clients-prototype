"""Deliberate type errors. pyright MUST reject every marked line.

The Magefile runs pyright with JSON output and requires an error on each line
marked "# should not type-check", so one failing line cannot mask the others.
"""

from __future__ import annotations

from datetime import datetime

from spicedb import Transaction, at_least, full

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
)
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
