"""Deliberate type errors. pyright MUST reject this file.

Each call site is annotated with the expected error class.
"""

from __future__ import annotations

from spicedb import at_least, full
from testdata.sync import TypedClient

from testdata.permissions import (
    Document,
    DocumentView,
    Team,
    TeamMember,
    User,
    IpRangeContext,
    TimeWindowContext,
)

# 1. Team (the resource ref) is not a DocumentViewerSubject; only TeamMember is.
_ = Document("readme").viewer(Team("eng"))

# 2. TeamMember cannot be passed where DocumentEditorSubject (= User) is required.
_ = Document("readme").editor(TeamMember("eng"))

# 3. Wrong caveat-context type for with_ip_range.
_ = User("alice").with_ip_range(TimeWindowContext(start="x", end="y"))

# 4. Wrong field type on IpRangeContext.
_ = IpRangeContext(allowed_cidr=42)

# Native token and named-mode contracts also apply to generated clients.
at_least("plain revision")
TypedClient.connect("remote.example:50051", "secret", remote_credentials=True)
def invalid_mode(client: TypedClient) -> None:
    client.check(full(), Document("readme").view, User("alice"), tracing=True)

    client.lookup_resources(full(), DocumentView, User("alice"), permissionship_filter=True)

    at_least(client.touch(Document("readme").viewer(User("alice"))))

    # Native capabilities must not add permissive schema overloads.
    client.check(full(), Document("one").view, Team("eng"))
    client.lookup_resources(full(), DocumentView, Team("eng"), page_size=2)
    client.lookup_subjects(full(), Document("one").view, Team, limit=2)
    client.watch(filters=["document"])
    client.delete_relationships("document", limit=2)
