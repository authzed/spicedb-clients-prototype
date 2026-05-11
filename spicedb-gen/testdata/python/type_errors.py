"""Deliberate type errors. pyright MUST reject this file.

Each call site is annotated with the expected error class.
"""

from __future__ import annotations

from permissions import (
    Document,
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
