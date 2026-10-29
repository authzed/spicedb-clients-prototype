"""Strict reads of required response fields, shared by both client flavors.

Root DESIGN.md, "Expose native errors without losing server diagnostics": a
response the client cannot interpret fails with INTERNAL. A missing required
field is never skipped and never filled with an empty value -- a skipped item
makes a full page look short and silently ends a paged read early, and an
empty ID or revision reads as real data.

These helpers are for server responses only. Protos a caller hands to
``spicedb.protoconv`` are caller input and fail as local validation instead.
"""

from __future__ import annotations

from typing import Any

from spicedb.errors import InternalError
from spicedb.token import ZedToken


def malformed(what: str) -> InternalError:
    return InternalError(f"spicedb: malformed response: {what}")


def need(message: Any, field: str, what: str) -> Any:
    """``message.field``, which must be set."""
    if not message.HasField(field):
        raise malformed(f"{what} is missing {field}")
    return getattr(message, field)


def token(message: Any, field: str, what: str) -> ZedToken:
    """The revision in ``message.field``, which must be set and nonempty."""
    value = need(message, field, what).token
    if not value:
        raise malformed(f"{what}.{field} is empty")
    return ZedToken(value)


def text(value: str, what: str) -> str:
    """A string field that must be nonempty (an ID, a name)."""
    if not value:
        raise malformed(f"{what} is empty")
    return value


def object_ref(message: Any, field: str, what: str):
    """The object reference in ``message.field``: set, with a type and ID."""
    from spicedb.types import ObjectRef

    ref = need(message, field, what)
    return ObjectRef._unchecked(
        text(ref.object_type, f"{what}.{field}.object_type"),
        text(ref.object_id, f"{what}.{field}.object_id"),
    )


def relationship(message: Any, field: str, what: str):
    """The relationship in ``message.field`` (or ``message`` itself when
    ``field`` is None), with its resource, relation and subject object."""
    from spicedb.types import Relationship

    rel = message if field is None else need(message, field, what)
    where = what if field is None else f"{what}.{field}"
    object_ref(rel, "resource", where)
    text(rel.relation, f"{where}.relation")
    subject = need(rel, "subject", where)
    object_ref(subject, "object", f"{where}.subject")
    return Relationship._from_proto(rel)
