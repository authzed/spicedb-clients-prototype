"""Native immutable request and result values. Transaction is a mutable builder."""

from __future__ import annotations
from spicedb.token import ZedToken, _token_string

import json
import re
from typing import TYPE_CHECKING

from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field, fields, replace
from datetime import datetime, timedelta, timezone
from enum import Enum
from math import isfinite
from types import MappingProxyType
from typing import TypeAlias
from authzed.api.v1 import core_pb2, permission_service_pb2, schema_service_pb2
from google.protobuf import struct_pb2, timestamp_pb2
from google.protobuf.message import Message
from spicedb.errors import LocalValidationError

if TYPE_CHECKING:
    from spicedb.errors import SpiceDBError

JSONValue: TypeAlias = (
    None | bool | str | int | float | Sequence["JSONValue"] | Mapping[str, "JSONValue"]
)
JSONObject: TypeAlias = Mapping[str, JSONValue]


def _snapshot(value):
    if isinstance(value, Mapping):
        return MappingProxyType({k: _snapshot(v) for k, v in value.items()})
    if isinstance(value, Sequence) and (not isinstance(value, (str, bytes, bytearray))):
        return tuple((_snapshot(v) for v in value))
    return value


def _freeze(self) -> None:
    for item in fields(self):
        object.__setattr__(self, item.name, _snapshot(getattr(self, item.name)))


def _json(value, path, active=None):
    if active is None:
        active = set()
    if value is None or isinstance(value, (str, bool)):
        return value
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        try:
            valid = isfinite(value) and (
                not isinstance(value, int) or int(float(value)) == value
            )
        except (OverflowError, ValueError):
            valid = False
        if valid:
            return value
    if isinstance(value, Mapping) or (
        isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray))
    ):
        identity = id(value)
        if identity in active:
            raise LocalValidationError(f"{path}: cyclic JSON collection")
        active.add(identity)
        try:
            if isinstance(value, Mapping):
                result = {}
                for key, item in value.items():
                    if not isinstance(key, str):
                        raise LocalValidationError(
                            f"{path}: JSON key {key!r} must be a string"
                        )
                    result[key] = _json(item, f"{path}.{key}", active)
                return result
            return [
                _json(item, f"{path}[{index}]", active)
                for index, item in enumerate(value)
            ]
        finally:
            active.remove(identity)
    raise LocalValidationError(
        f"{path}: cannot represent {type(value).__name__} as JSON"
    )


def context_to_struct(context: JSONObject, what: str) -> struct_pb2.Struct:
    converted = _json(context, what)
    if not isinstance(converted, dict):
        raise LocalValidationError(f"{what}: must be a JSON object")
    result = struct_pb2.Struct()
    result.update(converted)
    return result


def context_from_struct(context: struct_pb2.Struct) -> JSONObject:

    def convert(v):
        kind = v.WhichOneof("kind")
        if kind == "null_value":
            return None
        if kind == "struct_value":
            return {k: convert(x) for k, x in v.struct_value.fields.items()}
        if kind == "list_value":
            return [convert(x) for x in v.list_value.values]
        return getattr(v, kind) if kind else None

    return _snapshot({k: convert(v) for k, v in context.fields.items()})


def _format_expiration(value: datetime) -> str:
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    value = value.astimezone(timezone.utc)
    text = value.strftime("%Y-%m-%dT%H:%M:%S")
    if value.microsecond:
        text += f".{value.microsecond:06d}".rstrip("0")
    return text + "Z"


_EXPIRATION_PREFIX = "[expiration:"


def _parse_tuple_suffix(suffix: str, text: str):
    caveat = None
    expiration = None
    if suffix and (
        not suffix.startswith(_EXPIRATION_PREFIX)
        or suffix.startswith(_EXPIRATION_PREFIX + "{")
    ):
        body = suffix[1:]
        end = min((i for i in (body.find(":"), body.find("]")) if i >= 0), default=-1)
        if end <= 0:
            raise LocalValidationError(f"relationship: invalid tuple caveat in {text!r}")
        name = body[:end]
        if body[end] == "]":
            caveat = Caveat(name)
            suffix = body[end + 1 :]
        else:
            rest = body[end + 1 :]
            try:
                context, consumed = json.JSONDecoder().raw_decode(rest)
            except ValueError as e:
                raise LocalValidationError(
                    f"relationship: invalid tuple caveat context in {text!r}"
                ) from e
            if not isinstance(context, dict) or not rest[consumed:].startswith("]"):
                raise LocalValidationError(f"relationship: invalid tuple caveat in {text!r}")
            caveat = Caveat(name, context)
            suffix = rest[consumed + 1 :]
    if suffix:
        if not suffix.startswith(_EXPIRATION_PREFIX) or not suffix.endswith("]"):
            raise LocalValidationError(f"relationship: invalid tuple suffix {suffix!r}")
        raw = suffix[len(_EXPIRATION_PREFIX) : -1]
        try:
            expiration = datetime.fromisoformat(raw)
        except ValueError as e:
            raise LocalValidationError(f"relationship: invalid tuple expiration {raw!r}") from e
        if expiration.tzinfo is None or "T" not in raw.upper():
            raise LocalValidationError(
                f"relationship: tuple expiration {raw!r} must be RFC 3339 with a time zone"
            )
        expiration = expiration.astimezone(timezone.utc)
    return caveat, expiration


# SpiceDB's own tuple-syntax rules (pkg/tuple): a definition name, optionally
# prefixed by `prefix/` segments, and an object ID (or `*`, the wildcard).
# The server rejects anything else, so the client rejects it before sending.
_OBJECT_TYPE_RE = re.compile(
    r"([a-z][a-z0-9_]{1,61}[a-z0-9]/)*[a-z][a-z0-9_]{1,62}[a-z0-9]"
)
_OBJECT_ID_RE = re.compile(r"[a-zA-Z0-9/_|\-=+]+|\*")


@dataclass(frozen=True)
class ObjectRef:
    """An object, ``type:id``.

    ``object_type`` and ``object_id`` follow SpiceDB's tuple syntax: a
    lowercase definition name (optionally ``prefix/name``) and a nonempty ID
    of ``a-z A-Z 0-9 / _ | - = +`` characters, or ``*`` for a wildcard.
    Anything else raises :class:`~spicedb.errors.LocalValidationError` (an
    ``InvalidArgumentError`` and a ``ValueError``), since SpiceDB would reject
    it. Values the server returns are taken as sent.
    """

    object_type: str
    object_id: str

    @classmethod
    def parse(cls, text: str) -> ObjectRef:
        """Parse ``type:id``, e.g. ``ObjectRef.parse("document:readme")``."""
        if not isinstance(text, str):
            raise LocalValidationError(f"object: expected type:id string, got {text!r}")
        object_type, sep, object_id = text.partition(":")
        if not sep:
            raise LocalValidationError(f"object: invalid {text!r}; expected type:id")
        return cls(object_type, object_id)

    @classmethod
    def _unchecked(cls, object_type: str, object_id: str) -> ObjectRef:
        """A ref from server data, kept exactly as sent (no local validation)."""
        value = object.__new__(cls)
        object.__setattr__(value, "object_type", object_type)
        object.__setattr__(value, "object_id", object_id)
        return value

    def _to_proto(self):
        return core_pb2.ObjectReference(
            object_type=self.object_type, object_id=self.object_id
        )

    def __post_init__(self) -> None:
        if not isinstance(self.object_type, str) or not _OBJECT_TYPE_RE.fullmatch(
            self.object_type
        ):
            raise LocalValidationError(
                f"object.object_type: invalid {self.object_type!r}; expected a "
                "lowercase definition name such as 'document' or 'prefix/document'"
            )
        if not isinstance(self.object_id, str) or not _OBJECT_ID_RE.fullmatch(
            self.object_id
        ):
            raise LocalValidationError(
                f"object.object_id: invalid {self.object_id!r}; expected a nonempty "
                "ID of a-z A-Z 0-9 / _ | - = + characters, or *"
            )
        _freeze(self)

    def __str__(self) -> str:
        """``type:id``, as in SpiceDB's tuple syntax."""
        return f"{self.object_type}:{self.object_id}"


@dataclass(frozen=True)
class SubjectRef:
    object: ObjectRef
    relation: str | None = None

    @classmethod
    def parse(cls, text: str) -> SubjectRef:
        """Parse ``type:id`` or ``type:id#relation``.

        ``type:id#...`` is a direct subject (relation None), as in SpiceDB's
        tuple syntax. Invalid input raises
        :class:`~spicedb.errors.LocalValidationError`.
        """
        if not isinstance(text, str):
            raise LocalValidationError(
                f"subject: expected type:id[#relation] string, got {text!r}"
            )
        obj, sep, relation = text.partition("#")
        if sep and (not relation or "#" in relation):
            raise LocalValidationError(
                f"subject: invalid {text!r}; expected type:id[#relation]"
            )
        return cls(ObjectRef.parse(obj), relation if sep and relation != "..." else None)

    def __post_init__(self) -> None:
        if self.relation == "":
            raise LocalValidationError("subject.relation: must be nonempty or None")
        _freeze(self)

    def _to_proto(self):
        return core_pb2.SubjectReference(
            object=self.object._to_proto(), optional_relation=self.relation or ""
        )

    def __str__(self) -> str:
        """``type:id`` or ``type:id#relation``, as in SpiceDB's tuple syntax."""
        return str(self.object) + (f"#{self.relation}" if self.relation else "")


@dataclass(frozen=True)
class Caveat:
    name: str
    context: JSONObject | None = None

    def __post_init__(self) -> None:
        if self.context is not None:
            context_to_struct(self.context, "caveat.context")
        _freeze(self)


@dataclass(frozen=True)
class PermissionCheck:
    resource: ObjectRef
    permission: str
    subject: SubjectRef
    context: JSONObject | None = None

    def __post_init__(self) -> None:
        if self.context is not None:
            context_to_struct(self.context, "check.context")
        _freeze(self)


@dataclass(frozen=True)
class Relationship:
    resource: ObjectRef
    relation: str
    subject: SubjectRef
    caveat: Caveat | None = None
    expiration: datetime | None = None

    @classmethod
    def from_triple(
        cls,
        resource: str,
        relation: str,
        subject: str,
        *,
        caveat: Caveat | None = None,
        expiration: datetime | None = None,
    ) -> Relationship:
        try:
            rt, ri = resource.split(":", 1)
            subj, sep, sr = subject.partition("#")
            st, si = subj.split(":", 1)
            if not all((rt, ri, relation, st, si)) or (sep and (not sr)):
                raise ValueError()
        except ValueError as e:
            raise LocalValidationError(
                "relationship: expected type:id, relation, type:id[#relation]"
            ) from e
        return cls(
            ObjectRef(rt, ri),
            relation,
            SubjectRef(ObjectRef(st, si), sr if sep and sr != "..." else None),
            caveat,
            expiration,
        )

    @classmethod
    def from_tuple(cls, text: str) -> Relationship:
        """Parse SpiceDB's tuple syntax.

        ``resource_type:resource_id#relation@subject_type:subject_id[#subject_relation]``
        followed by an optional ``[caveat_name]`` or ``[caveat_name:{json}]``
        and an optional ``[expiration:<RFC 3339>]``, in that order. A subject
        relation of ``...`` means a direct subject. ``str(relationship)``
        produces this form, so the two round-trip. Fractional seconds beyond
        microseconds are truncated (Python's ``datetime`` resolution).
        """
        if not isinstance(text, str):
            raise LocalValidationError("relationship: expected tuple string")
        main, bracket, suffix = text.partition("[")
        suffix = bracket + suffix
        try:
            left, right = main.split("@")
            resource, relation = left.split("#")
            subject, sep, subject_relation = right.partition("#")
            if "#" in subject_relation:
                raise ValueError()
            rt, ri = resource.split(":", 1)
            st, si = subject.split(":", 1)
            if not all((rt, ri, relation, st, si)) or (sep and not subject_relation):
                raise ValueError()
        except ValueError as e:
            raise LocalValidationError(
                f"relationship: invalid tuple {text!r}; expected type:id#relation@type:id[#relation]"
            ) from e
        caveat, expiration = _parse_tuple_suffix(suffix, text)
        return cls(
            ObjectRef(rt, ri),
            relation,
            SubjectRef(
                ObjectRef(st, si),
                subject_relation if sep and subject_relation != "..." else None,
            ),
            caveat,
            expiration,
        )

    def __str__(self) -> str:
        """SpiceDB tuple syntax, including caveat and expiration.

        ``document:readme#viewer@user:alice[ip:{"cidr":"10.0.0.0/8"}][expiration:2030-01-01T00:00:00Z]``.
        Caveat context keys are sorted; expiration is UTC RFC 3339.
        :meth:`from_tuple` parses the result.
        """
        text = f"{self.resource}#{self.relation}@{self.subject}"
        if self.caveat is not None:
            text += "[" + self.caveat.name
            if self.caveat.context:
                text += ":" + json.dumps(
                    _json(self.caveat.context, "caveat.context"),
                    sort_keys=True,
                    separators=(",", ":"),
                )
            text += "]"
        if self.expiration is not None:
            text += f"[expiration:{_format_expiration(self.expiration)}]"
        return text

    def to_dict(self) -> dict[str, JSONValue]:
        """A JSON-compatible diagnostic form for logs (not a wire format)."""
        value: dict[str, JSONValue] = {
            "resource": str(self.resource),
            "relation": self.relation,
            "subject": str(self.subject),
        }
        if self.caveat is not None:
            value["caveat_name"] = self.caveat.name
            if self.caveat.context is not None:
                value["caveat_context"] = _json(self.caveat.context, "caveat.context")
        if self.expiration is not None:
            value["expiration"] = _format_expiration(self.expiration)
        return value

    @property
    def resource_type(self) -> str:
        """Short for ``resource.object_type``."""
        return self.resource.object_type

    @property
    def resource_id(self) -> str:
        """Short for ``resource.object_id``."""
        return self.resource.object_id

    @property
    def subject_type(self) -> str:
        """Short for ``subject.object.object_type``."""
        return self.subject.object.object_type

    @property
    def subject_id(self) -> str:
        """Short for ``subject.object.object_id``."""
        return self.subject.object.object_id

    @property
    def subject_relation(self) -> str | None:
        """Short for ``subject.relation`` (None for a direct subject)."""
        return self.subject.relation

    def with_resource(self, resource: ObjectRef) -> Relationship:
        """A copy on a different resource; caveat and expiration are kept."""
        return replace(self, resource=resource)

    def with_relation(self, relation: str) -> Relationship:
        """A copy with a different relation; caveat and expiration are kept."""
        return replace(self, relation=relation)

    def with_subject(self, subject: SubjectRef) -> Relationship:
        """A copy with a different subject; caveat and expiration are kept."""
        return replace(self, subject=subject)

    def to_filter(self) -> Filter:
        relation = (
            SubjectRelationFilter.direct()
            if self.subject.relation is None
            else SubjectRelationFilter.named(self.subject.relation)
        )
        return Filter(
            self.resource.object_type,
            resource_id=self.resource.object_id,
            relation=self.relation,
            subject=SubjectFilter(
                self.subject.object.object_type,
                subject_id=self.subject.object.object_id,
                relation=relation,
            ),
        )

    def _to_proto(self):
        caveat = None
        if self.caveat:
            caveat = core_pb2.ContextualizedCaveat(
                caveat_name=self.caveat.name,
                context=None
                if self.caveat.context is None
                else context_to_struct(self.caveat.context, "caveat.context"),
            )
        ts = None
        if self.expiration is not None:
            ts = timestamp_pb2.Timestamp()
            ts.FromDatetime(self.expiration)
        return core_pb2.Relationship(
            resource=self.resource._to_proto(),
            relation=self.relation,
            subject=self.subject._to_proto(),
            optional_caveat=caveat,
            optional_expires_at=ts,
        )

    @classmethod
    def _from_proto(cls, p):
        caveat = (
            Caveat(
                p.optional_caveat.caveat_name,
                context_from_struct(p.optional_caveat.context)
                if p.optional_caveat.HasField("context")
                else None,
            )
            if p.HasField("optional_caveat")
            else None
        )
        return cls(
            ObjectRef._unchecked(p.resource.object_type, p.resource.object_id),
            p.relation,
            SubjectRef(
                ObjectRef._unchecked(p.subject.object.object_type, p.subject.object.object_id),
                p.subject.optional_relation or None,
            ),
            caveat,
            p.optional_expires_at.ToDatetime(tzinfo=timezone.utc)
            if p.HasField("optional_expires_at")
            else None,
        )

    def __post_init__(self) -> None:
        _freeze(self)


class SubjectRelationMode(Enum):
    ANY = "any"
    DIRECT = "direct"
    NAMED = "named"


@dataclass(frozen=True)
class SubjectRelationFilter:
    kind: SubjectRelationMode = SubjectRelationMode.ANY
    name: str | None = None

    def __post_init__(self) -> None:
        if (
            not isinstance(self.kind, SubjectRelationMode)
            or (self.kind == SubjectRelationMode.NAMED and (not self.name))
            or (self.kind != SubjectRelationMode.NAMED and self.name is not None)
        ):
            raise LocalValidationError("subject.relation: invalid relation filter")
        _freeze(self)

    @classmethod
    def any(cls) -> SubjectRelationFilter:
        return cls()

    @classmethod
    def direct(cls) -> SubjectRelationFilter:
        return cls(SubjectRelationMode.DIRECT)

    @classmethod
    def named(cls, name: str) -> SubjectRelationFilter:
        return cls(SubjectRelationMode.NAMED, name)

    def __str__(self) -> str:
        """``*`` (any), ``...`` (direct) or the relation name."""
        if self.kind == SubjectRelationMode.DIRECT:
            return "..."
        if self.kind == SubjectRelationMode.NAMED:
            return self.name or ""
        return "*"


@dataclass(frozen=True)
class SubjectFilter:
    subject_type: str
    subject_id: str | None = field(default=None, kw_only=True)
    relation: SubjectRelationFilter = field(
        default_factory=SubjectRelationFilter.any, kw_only=True
    )

    def __post_init__(self) -> None:
        if not self.subject_type:
            raise LocalValidationError("subject.subject_type: required")
        _freeze(self)

    def _to_proto(self):
        rel = (
            None
            if self.relation.kind == SubjectRelationMode.ANY
            else permission_service_pb2.SubjectFilter.RelationFilter(
                relation=self.relation.name or ""
            )
        )
        return permission_service_pb2.SubjectFilter(
            subject_type=self.subject_type,
            optional_subject_id=self.subject_id or "",
            optional_relation=rel,
        )


@dataclass(frozen=True)
class Filter:
    resource_type: str
    resource_id: str | None = field(default=None, kw_only=True)
    resource_id_prefix: str | None = field(default=None, kw_only=True)
    relation: str | None = field(default=None, kw_only=True)
    subject: SubjectFilter | None = field(default=None, kw_only=True)

    def __post_init__(self) -> None:
        if self.resource_id is not None and self.resource_id_prefix is not None:
            raise LocalValidationError(
                "filter.resource_id and resource_id_prefix are mutually exclusive"
            )
        _freeze(self)

    def _to_proto(self):
        return permission_service_pb2.RelationshipFilter(
            resource_type=self.resource_type,
            optional_resource_id=self.resource_id or "",
            optional_resource_id_prefix=self.resource_id_prefix or "",
            optional_relation=self.relation or "",
            optional_subject_filter=self.subject._to_proto() if self.subject else None,
        )

    def matches(self, relationship: Relationship) -> bool:
        """Whether SpiceDB would select ``relationship`` with this filter.

        Applies the server's semantics client-side: resource type, resource
        ID or ID prefix, relation, subject type and ID, and the subject
        relation's Any/Direct/Named distinction. Caveat and expiration are
        not filterable. Test doubles use this instead of reimplementing it.
        """
        if not isinstance(relationship, Relationship):
            raise LocalValidationError("filter.matches: expected Relationship")
        resource = relationship.resource
        if self.resource_type and self.resource_type != resource.object_type:
            return False
        if self.resource_id and self.resource_id != resource.object_id:
            return False
        if self.resource_id_prefix and not resource.object_id.startswith(self.resource_id_prefix):
            return False
        if self.relation and self.relation != relationship.relation:
            return False
        if self.subject is None:
            return True
        subject = relationship.subject
        if self.subject.subject_type != subject.object.object_type:
            return False
        if self.subject.subject_id and self.subject.subject_id != subject.object.object_id:
            return False
        kind = self.subject.relation.kind
        if kind == SubjectRelationMode.DIRECT:
            return subject.relation is None
        if kind == SubjectRelationMode.NAMED:
            return subject.relation == self.subject.relation.name
        return True

    def __str__(self) -> str:
        """A zed-like form with ``*`` for unset fields, e.g. ``document:*#viewer@user:alice``."""
        resource_id = self.resource_id or "*"
        if self.resource_id_prefix:
            resource_id = f"{self.resource_id_prefix}%*"
        text = f"{self.resource_type or '*'}:{resource_id}#{self.relation or '*'}@"
        if self.subject is None:
            return text + "*"
        text += f"{self.subject.subject_type}:{self.subject.subject_id or '*'}"
        if self.subject.relation.kind != SubjectRelationMode.ANY:
            text += "#" + str(self.subject.relation)
        return text

    def to_dict(self) -> dict[str, JSONValue]:
        """A JSON-compatible diagnostic form; unset fields are omitted."""
        value: dict[str, JSONValue] = {"resource_type": self.resource_type}
        for name in ("resource_id", "resource_id_prefix", "relation"):
            if getattr(self, name) is not None:
                value[name] = getattr(self, name)
        if self.subject is not None:
            value["subject_type"] = self.subject.subject_type
            if self.subject.subject_id is not None:
                value["subject_id"] = self.subject.subject_id
            value["subject_relation"] = str(self.subject.relation)
        return value


@dataclass(frozen=True, kw_only=True)
class SchemaFilter:
    definition_name: str | None = None
    caveat_name: str | None = None
    relation_name: str | None = None
    permission_name: str | None = None

    def _to_proto(self):
        return schema_service_pb2.ReflectionSchemaFilter(
            optional_definition_name_filter=self.definition_name or "",
            optional_caveat_name_filter=self.caveat_name or "",
            optional_relation_name_filter=self.relation_name or "",
            optional_permission_name_filter=self.permission_name or "",
        )

    def __post_init__(self) -> None:
        _freeze(self)


class UpdateOperation(Enum):
    """The kind of mutation an `Update` represents.

    Used both for changes observed through `watch()` and for updates queued
    in a `Transaction` (`create`/`touch`/`delete`, or a prebuilt `Update`
    passed to `Transaction.add`). `UNSPECIFIED` only appears on observed
    updates from a server sending an unknown operation; it cannot be written.
    """

    CREATE = "create"
    TOUCH = "touch"
    DELETE = "delete"
    UNSPECIFIED = "unspecified"


_UPDATE_OP_MAP = {
    core_pb2.RelationshipUpdate.OPERATION_CREATE: UpdateOperation.CREATE,
    core_pb2.RelationshipUpdate.OPERATION_TOUCH: UpdateOperation.TOUCH,
    core_pb2.RelationshipUpdate.OPERATION_DELETE: UpdateOperation.DELETE,
    core_pb2.RelationshipUpdate.OPERATION_UNSPECIFIED: UpdateOperation.UNSPECIFIED,
}
_UPDATE_OP_TO_PROTO = {
    UpdateOperation.CREATE: core_pb2.RelationshipUpdate.OPERATION_CREATE,
    UpdateOperation.TOUCH: core_pb2.RelationshipUpdate.OPERATION_TOUCH,
    UpdateOperation.DELETE: core_pb2.RelationshipUpdate.OPERATION_DELETE,
}


@dataclass(frozen=True)
class Update:
    """A single relationship mutation: observed via ``watch()``, or queued in a ``Transaction``."""

    operation: UpdateOperation
    relationship: Relationship

    @staticmethod
    def _from_proto(u: core_pb2.RelationshipUpdate) -> "Update":
        """Create from a proto RelationshipUpdate."""
        op = _UPDATE_OP_MAP.get(u.operation, UpdateOperation.UNSPECIFIED)
        return Update(
            operation=op, relationship=Relationship._from_proto(u.relationship)
        )

    def _to_proto(self) -> core_pb2.RelationshipUpdate:
        operation = _UPDATE_OP_TO_PROTO.get(self.operation)
        if operation is None:
            raise LocalValidationError("update.operation: cannot send UNSPECIFIED")
        return core_pb2.RelationshipUpdate(
            operation=operation, relationship=self.relationship._to_proto()
        )

    def __post_init__(self) -> None:
        if not isinstance(self.operation, UpdateOperation):
            raise LocalValidationError("update.operation: expected UpdateOperation")
        if not isinstance(self.relationship, Relationship):
            raise LocalValidationError("update.relationship: expected Relationship")
        _freeze(self)

    def __str__(self) -> str:
        """``OPERATION(relationship)``, e.g. ``TOUCH(document:a#viewer@user:b)``."""
        return f"{self.operation.name}({self.relationship})"

    def to_dict(self) -> dict[str, JSONValue]:
        """A JSON-compatible diagnostic form for logs."""
        return {"operation": self.operation.name, "relationship": self.relationship.to_dict()}


class PreconditionOperation(Enum):
    """Whether a write precondition requires a match or the absence of one."""

    MUST_MATCH = "must_match"
    MUST_NOT_MATCH = "must_not_match"


_PRECONDITION_TO_PROTO = {
    PreconditionOperation.MUST_MATCH: permission_service_pb2.Precondition.OPERATION_MUST_MATCH,
    PreconditionOperation.MUST_NOT_MATCH: permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
}


@dataclass(frozen=True)
class Precondition:
    """A write or delete precondition: a filter that must (not) match."""

    operation: PreconditionOperation
    filter: Filter

    def __post_init__(self) -> None:
        if not isinstance(self.operation, PreconditionOperation):
            raise LocalValidationError("precondition.operation: expected PreconditionOperation")
        if not isinstance(self.filter, Filter):
            raise LocalValidationError("precondition.filter: expected Filter")

    def _to_proto(self) -> permission_service_pb2.Precondition:
        return permission_service_pb2.Precondition(
            operation=_PRECONDITION_TO_PROTO[self.operation], filter=self.filter._to_proto()
        )

    def __str__(self) -> str:
        """``OPERATION(filter)``, e.g. ``MUST_MATCH(document:a#*@*)``."""
        return f"{self.operation.name}({self.filter})"

    def to_dict(self) -> dict[str, JSONValue]:
        """A JSON-compatible diagnostic form for logs."""
        return {"operation": self.operation.name, "filter": str(self.filter)}


@dataclass(frozen=True)
class WatchEvent:
    """A single event yielded by `SpiceDBClient.watch()`.

    `changes_through` is the ZedToken this event is current through. Pass it
    as `start_revision` to a later `watch()` call to resume after a dropped
    stream -- without it, a consumer whose stream dies can only restart from
    its original token (reprocessing everything since, possibly past the GC
    window) or from head (silently losing every change in the gap).

    A checkpoint event (`is_checkpoint=True`) carries no `updates` -- it
    exists only to advertise a fresh resume point and, behind a proxy that
    aborts idle connections, to keep the stream alive. Request checkpoints
    with `watch(content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS))`.
    """

    updates: Sequence[Update]
    changes_through: ZedToken
    is_checkpoint: bool = False
    transaction_metadata: JSONObject | None = None
    schema_updated: bool = False
    full_revision_metadata: Sequence[JSONObject] = ()

    def __post_init__(self) -> None:
        _freeze(self)


def _checked_update(update: Update) -> Update:
    if not isinstance(update, Update):
        raise LocalValidationError(f"update: expected Update, got {update!r}")
    update._to_proto()  # validate now, not at write time
    return update


class Transaction:
    """A mutable builder batching relationship writes with preconditions.

    ``len(txn)`` is the number of updates (so an empty transaction is
    falsy); ``updates`` and ``preconditions`` are immutable snapshots.
    ``str(txn)`` lists them readably and ``to_dict()`` gives a JSON-able form.
    """

    __slots__ = ("_update_list", "_precondition_list")

    def __init__(self) -> None:
        self._update_list: list[Update] = []
        self._precondition_list: list[Precondition] = []

    def _add(self, operation: UpdateOperation, rel: Relationship) -> Transaction:
        update = Update(operation, rel)
        update._to_proto()  # validate now, not at write time
        self._update_list.append(update)
        return self

    def create(self, rel: Relationship) -> Transaction:
        """Add a CREATE operation (fails if relationship already exists)."""
        return self._add(UpdateOperation.CREATE, rel)

    def touch(self, rel: Relationship) -> Transaction:
        """Add a TOUCH operation (create or update)."""
        return self._add(UpdateOperation.TOUCH, rel)

    def delete(self, rel: Relationship) -> Transaction:
        """Add a DELETE operation."""
        return self._add(UpdateOperation.DELETE, rel)

    def add(self, update: Update) -> Transaction:
        """Queue a prebuilt ``Update`` (``CREATE``, ``TOUCH`` or ``DELETE``).

        Equivalent to the matching ``create``/``touch``/``delete`` call, for
        code that already holds ``Update`` values (a work queue, a replayed
        watch event). An ``UNSPECIFIED`` operation or a non-``Update`` raises
        :class:`~spicedb.errors.LocalValidationError` and queues nothing.
        """
        self._update_list.append(_checked_update(update))
        return self

    def extend(self, updates: Iterable[Update]) -> Transaction:
        """Queue each prebuilt ``Update`` in order, as ``add`` does.

        All-or-nothing: if any item is invalid, it raises
        :class:`~spicedb.errors.LocalValidationError` and none are queued.
        """
        if isinstance(updates, (Update, str, bytes)):
            raise LocalValidationError("updates: expected an iterable of Update values")
        checked = [_checked_update(update) for update in updates]
        self._update_list.extend(checked)
        return self

    def must_not_match(self, f: Filter) -> Transaction:
        """Add a precondition that no relationships match the filter."""
        precondition = Precondition(PreconditionOperation.MUST_NOT_MATCH, f)
        precondition._to_proto()
        self._precondition_list.append(precondition)
        return self

    def must_match(self, f: Filter) -> Transaction:
        """Add a precondition that at least one relationship matches the filter."""
        precondition = Precondition(PreconditionOperation.MUST_MATCH, f)
        precondition._to_proto()
        self._precondition_list.append(precondition)
        return self

    @property
    def updates(self) -> tuple[Update, ...]:
        """A snapshot of the queued updates, in order."""
        return tuple(self._update_list)

    @property
    def preconditions(self) -> tuple[Precondition, ...]:
        """A snapshot of the queued preconditions, in order."""
        return tuple(self._precondition_list)

    def is_idempotent(self) -> bool:
        """Whether replaying this transaction is safe: it holds only touches
        and no preconditions. This is the rule the client uses to decide
        whether a ``write`` may be retried."""
        return not self._precondition_list and all(
            update.operation is UpdateOperation.TOUCH for update in self._update_list
        )

    def __len__(self) -> int:
        return len(self._update_list)

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, Transaction):
            return NotImplemented
        return (self._update_list, self._precondition_list) == (
            other._update_list,
            other._precondition_list,
        )

    __hash__ = None  # type: ignore[assignment]  # mutable builder

    def __str__(self) -> str:
        parts = [str(p) for p in self._precondition_list] + [str(u) for u in self._update_list]
        return "[" + ", ".join(parts) + "]"

    def __repr__(self) -> str:
        return f"Transaction(preconditions={self.preconditions!r}, updates={self.updates!r})"

    def to_dict(self) -> dict[str, JSONValue]:
        """A JSON-compatible diagnostic form for logs."""
        return {
            "preconditions": [p.to_dict() for p in self._precondition_list],
            "updates": [u.to_dict() for u in self._update_list],
        }

    def _update_protos(self) -> list[core_pb2.RelationshipUpdate]:
        return [u._to_proto() for u in self._update_list]

    def _precondition_protos(self) -> list[permission_service_pb2.Precondition]:
        return [p._to_proto() for p in self._precondition_list]


class TreeOperation(Enum):
    """The set operation combining an `IntermediateNode`'s children."""

    UNSPECIFIED = 0
    UNION = 1
    INTERSECTION = 2
    EXCLUSION = 3


@dataclass(frozen=True)
class IntermediateNode:
    """Combines child subtrees with a set operation."""

    operation: TreeOperation
    children: Sequence["PermissionTree"]

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class LeafNode:
    """Holds the concrete subjects at a leaf of a `PermissionTree`."""

    subjects: Sequence[SubjectRef]

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class PermissionTree:
    """A native node of an expanded permission tree.

    Exactly one of `intermediate` or `leaf` is non-None.
    """

    expanded_object: ObjectRef
    expanded_relation: str
    intermediate: IntermediateNode | None = None
    leaf: LeafNode | None = None

    def __post_init__(self) -> None:
        _freeze(self)


_TREE_OPERATION_MAP = {
    core_pb2.AlgebraicSubjectSet.OPERATION_UNSPECIFIED: TreeOperation.UNSPECIFIED,
    core_pb2.AlgebraicSubjectSet.OPERATION_UNION: TreeOperation.UNION,
    core_pb2.AlgebraicSubjectSet.OPERATION_INTERSECTION: TreeOperation.INTERSECTION,
    core_pb2.AlgebraicSubjectSet.OPERATION_EXCLUSION: TreeOperation.EXCLUSION,
}


def _permission_tree_from_proto(
    t: core_pb2.PermissionRelationshipTree,
) -> PermissionTree:
    """Recursively map a proto PermissionRelationshipTree to its native
    representation. Mirrors spicedb-go's `toPermissionTree` (client/expand_tree.go).
    """
    intermediate = None
    if t.HasField("intermediate"):
        intermediate = IntermediateNode(
            operation=_TREE_OPERATION_MAP.get(
                t.intermediate.operation, TreeOperation.UNSPECIFIED
            ),
            children=[
                _permission_tree_from_proto(child) for child in t.intermediate.children
            ],
        )
    leaf = None
    if t.HasField("leaf"):
        leaf = LeafNode(
            subjects=[
                SubjectRef(
                    object=ObjectRef._unchecked(s.object.object_type, s.object.object_id),
                    relation=s.optional_relation or None,
                )
                for s in t.leaf.subjects
            ]
        )
    return PermissionTree(
        expanded_object=ObjectRef._unchecked(
            t.expanded_object.object_type, t.expanded_object.object_id
        ),
        expanded_relation=t.expanded_relation,
        intermediate=intermediate,
        leaf=leaf,
    )


@dataclass(frozen=True)
class SchemaTypeReference:
    subject_definition_name: str
    caveat_name: str | None = None
    is_terminal_subject: bool = False
    relation_name: str | None = None
    is_public_wildcard: bool = False

    def __post_init__(self) -> None:
        _freeze(self)

    @classmethod
    def _from_proto(cls, value):
        return cls(
            value.subject_definition_name,
            value.optional_caveat_name or None,
            value.is_terminal_subject,
            value.optional_relation_name or None,
            value.is_public_wildcard,
        )


@dataclass(frozen=True)
class SchemaRelation:
    """A relation within a schema definition."""

    name: str
    comment: str
    parent_definition_name: str
    subject_types: Sequence[SchemaTypeReference] = ()

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionRelation
    ) -> "SchemaRelation":
        """Create from a proto ReflectionRelation."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            parent_definition_name=proto.parent_definition_name,
            subject_types=tuple(
                (
                    SchemaTypeReference._from_proto(value)
                    for value in proto.subject_types
                )
            ),
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaPermission:
    """A permission within a schema definition."""

    name: str
    comment: str
    parent_definition_name: str

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionPermission
    ) -> "SchemaPermission":
        """Create from a proto ReflectionPermission."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            parent_definition_name=proto.parent_definition_name,
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaCaveatParameter:
    """A parameter of a caveat."""

    name: str
    type: str
    parent_caveat_name: str

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionCaveatParameter
    ) -> "SchemaCaveatParameter":
        """Create from a proto ReflectionCaveatParameter."""
        return cls(
            name=proto.name,
            type=proto.type,
            parent_caveat_name=proto.parent_caveat_name,
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaDefinition:
    """A definition in a SpiceDB schema, including its relations and
    permissions."""

    name: str
    comment: str
    relations: Sequence[SchemaRelation]
    permissions: Sequence[SchemaPermission]

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionDefinition
    ) -> "SchemaDefinition":
        """Create from a proto ReflectionDefinition."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            relations=[SchemaRelation._from_proto(r) for r in proto.relations],
            permissions=[SchemaPermission._from_proto(p) for p in proto.permissions],
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaCaveat:
    """A caveat defined in a SpiceDB schema."""

    name: str
    comment: str
    expression: str
    parameters: Sequence[SchemaCaveatParameter]

    @classmethod
    def _from_proto(cls, proto: schema_service_pb2.ReflectionCaveat) -> "SchemaCaveat":
        """Create from a proto ReflectionCaveat."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            expression=proto.expression,
            parameters=[SchemaCaveatParameter._from_proto(p) for p in proto.parameters],
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class ReflectSchemaResult:
    """The result of a schema reflection call."""

    definitions: Sequence[SchemaDefinition]
    caveats: Sequence[SchemaCaveat]
    revision: ZedToken

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectSchemaResponse
    ) -> "ReflectSchemaResult":
        """Create from a proto ReflectSchemaResponse."""
        return cls(
            definitions=[SchemaDefinition._from_proto(d) for d in proto.definitions],
            caveats=[SchemaCaveat._from_proto(c) for c in proto.caveats],
            revision=ZedToken(proto.read_at.token),
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class RelationReference:
    """Identifies a relation or permission on a definition.

    Returned by `computable_permissions()`/`dependent_relations()`; mirrors
    spicedb-go's `RelationReference` (client/schema.go).
    """

    definition_name: str
    relation_name: str
    is_permission: bool

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionRelationReference
    ) -> "RelationReference":
        """Create from a proto ReflectionRelationReference (a server response)."""
        from spicedb import _wire

        return cls(
            definition_name=_wire.text(proto.definition_name, "ReflectionRelationReference.definition_name"),
            relation_name=_wire.text(proto.relation_name, "ReflectionRelationReference.relation_name"),
            is_permission=proto.is_permission,
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaDiff:
    """A single difference between two schemas.

    ``kind`` is a human-readable description of the diff type (e.g.
    "definition_added", "relation_removed", "permission_expr_changed") and
    the associated fields contain the details:
    - ``definition_name`` is set for definition and relation/permission-level
      diffs.
    - ``relation_name`` is set for relation-level diffs.
    - ``permission_name`` is set for permission-level diffs.
    - ``caveat_name`` is set for caveat-level diffs.
    """

    kind: str
    definition_name: str = ""
    relation_name: str = ""
    permission_name: str = ""
    caveat_name: str = ""
    details: JSONObject = field(default_factory=dict)

    def __post_init__(self) -> None:
        _freeze(self)


def _schema_diff_from_proto(
    proto: schema_service_pb2.ReflectionSchemaDiff,
) -> SchemaDiff:
    from dataclasses import replace
    from google.protobuf.json_format import MessageToDict

    summary = _schema_diff_summary(proto)
    return replace(
        summary, details=MessageToDict(proto, preserving_proto_field_name=True)
    )


def _schema_diff_summary(proto: schema_service_pb2.ReflectionSchemaDiff) -> SchemaDiff:
    """Map a single proto ReflectionSchemaDiff to its native representation.
    Mirrors spicedb-go's `schemaDiffFromProto` (client/schema.go).
    """
    kind = proto.WhichOneof("diff")
    if kind == "definition_added":
        return SchemaDiff(kind=kind, definition_name=proto.definition_added.name)
    if kind == "definition_removed":
        return SchemaDiff(kind=kind, definition_name=proto.definition_removed.name)
    if kind == "definition_doc_comment_changed":
        return SchemaDiff(
            kind=kind, definition_name=proto.definition_doc_comment_changed.name
        )
    if kind == "relation_added":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.relation_added.parent_definition_name,
            relation_name=proto.relation_added.name,
        )
    if kind == "relation_removed":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.relation_removed.parent_definition_name,
            relation_name=proto.relation_removed.name,
        )
    if kind == "relation_doc_comment_changed":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.relation_doc_comment_changed.parent_definition_name,
            relation_name=proto.relation_doc_comment_changed.name,
        )
    if kind == "relation_subject_type_added":
        rel = proto.relation_subject_type_added.relation
        return SchemaDiff(
            kind=kind,
            definition_name=rel.parent_definition_name,
            relation_name=rel.name,
        )
    if kind == "relation_subject_type_removed":
        rel = proto.relation_subject_type_removed.relation
        return SchemaDiff(
            kind=kind,
            definition_name=rel.parent_definition_name,
            relation_name=rel.name,
        )
    if kind == "permission_added":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.permission_added.parent_definition_name,
            permission_name=proto.permission_added.name,
        )
    if kind == "permission_removed":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.permission_removed.parent_definition_name,
            permission_name=proto.permission_removed.name,
        )
    if kind == "permission_doc_comment_changed":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.permission_doc_comment_changed.parent_definition_name,
            permission_name=proto.permission_doc_comment_changed.name,
        )
    if kind == "permission_expr_changed":
        return SchemaDiff(
            kind=kind,
            definition_name=proto.permission_expr_changed.parent_definition_name,
            permission_name=proto.permission_expr_changed.name,
        )
    if kind == "caveat_added":
        return SchemaDiff(kind=kind, caveat_name=proto.caveat_added.name)
    if kind == "caveat_removed":
        return SchemaDiff(kind=kind, caveat_name=proto.caveat_removed.name)
    if kind == "caveat_doc_comment_changed":
        return SchemaDiff(kind=kind, caveat_name=proto.caveat_doc_comment_changed.name)
    if kind == "caveat_expr_changed":
        return SchemaDiff(kind=kind, caveat_name=proto.caveat_expr_changed.name)
    if kind == "caveat_parameter_added":
        return SchemaDiff(
            kind=kind, caveat_name=proto.caveat_parameter_added.parent_caveat_name
        )
    if kind == "caveat_parameter_removed":
        return SchemaDiff(
            kind=kind, caveat_name=proto.caveat_parameter_removed.parent_caveat_name
        )
    if kind == "caveat_parameter_type_changed":
        return SchemaDiff(
            kind=kind,
            caveat_name=proto.caveat_parameter_type_changed.parameter.parent_caveat_name,
        )
    return SchemaDiff(kind="unknown")


class Permissionship(Enum):
    """Whether a check or lookup result reflects a full grant, a full
    denial, or is conditional on caveat context that was not fully evaluated
    by the server. Callers MUST check this before treating a result as a
    full grant — a CONDITIONAL_PERMISSION result may resolve to false once
    the missing caveat context is supplied.

    This enum serves both the check surface (`CheckResult`) and the lookup
    surface (`LookupResource`, `ResolvedSubject`). Lookups never yield
    NO_PERMISSION: a subject/resource pair that lacks the permission is
    simply absent from a lookup stream rather than yielded with that
    permissionship. NO_PERMISSION only appears on `CheckResult`, where the
    server is answering a yes/no/conditional question about one specific
    pair and "no" is itself an answer.

    NO_PERMISSION is appended after CONDITIONAL_PERMISSION (not inserted
    alongside UNSPECIFIED) so the values of the pre-existing members are not
    renumbered.
    """

    UNSPECIFIED = 0
    HAS_PERMISSION = 1
    CONDITIONAL_PERMISSION = 2
    NO_PERMISSION = 3


_PERMISSIONSHIP_MAP = {
    permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION: Permissionship.HAS_PERMISSION,
    permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION: Permissionship.CONDITIONAL_PERMISSION,
}


def _permissionship_from_proto(v: int) -> Permissionship:
    """Map the proto LookupPermissionship enum to its native equivalent.
    Unrecognized values map to Permissionship.UNSPECIFIED. Mirrors
    spicedb-go's `permissionshipFromProto` (client/lookup_types.go)."""
    return _PERMISSIONSHIP_MAP.get(v, Permissionship.UNSPECIFIED)


_CHECK_PERMISSIONSHIP_MAP = {
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION: Permissionship.HAS_PERMISSION,
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION: Permissionship.CONDITIONAL_PERMISSION,
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION: Permissionship.NO_PERMISSION,
}


def _check_permissionship_from_proto(v: int) -> Permissionship:
    """Map the proto CheckPermissionResponse.Permissionship enum (which,
    unlike LookupPermissionship, has a NO_PERMISSION value) to its native
    equivalent. Unrecognized values map to Permissionship.UNSPECIFIED.
    Mirrors spicedb-go's `checkPermissionshipFromProto`
    (client/check_types.go)."""
    return _CHECK_PERMISSIONSHIP_MAP.get(v, Permissionship.UNSPECIFIED)


@dataclass(frozen=True)
class PartialCaveatInfo:
    """Caveat context that was missing to fully evaluate a conditional
    result."""

    missing_required_context: Sequence[str]

    def __post_init__(self) -> None:
        _freeze(self)


def _partial_caveat_from_proto(parent: Message) -> PartialCaveatInfo | None:
    """Map a proto message's `partial_caveat_info` field to its native
    equivalent. An unset field maps to None. Mirrors spicedb-go's
    `partialCaveatFromProto` (client/lookup_types.go); see the module-level
    note above for why this takes the containing message rather than the
    submessage directly."""
    if not parent.HasField("partial_caveat_info"):
        return None
    return PartialCaveatInfo(
        missing_required_context=list(
            parent.partial_caveat_info.missing_required_context
        )
    )


@dataclass(frozen=True)
class LookupResource:
    """One result from `SpiceDBClient.lookup_resources()`."""

    resource_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None
    looked_up_at: ZedToken = ZedToken("")
    after_result_cursor: str | None = None
    """Pass as ``lookup_resources(cursor=...)`` with the same request and page size to resume after this item."""

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class CheckResult:
    """The outcome of a permission check. `permissionship` carries the
    server's three-valued answer — a CONDITIONAL_PERMISSION result means the
    server needed caveat context that was not supplied and is NOT a grant.
    Prefer `has_permission` for the common case.
    """

    permissionship: Permissionship
    missing_context: Sequence[str]
    checked_at: ZedToken
    debug_trace: DebugInformation | None = None
    expiration: datetime | None = None
    error: SpiceDBError | None = None
    """The per-item error under ``check_bulk(item_errors=BulkItemErrors.RETURN)``.

    When set, ``permissionship`` is UNSPECIFIED and the result never grants."""

    @property
    def has_permission(self) -> bool:
        """Whether the subject has the permission outright. False for a
        CONDITIONAL_PERMISSION result -- the server could not evaluate the
        caveat, so treating it as a grant would authorize on an unevaluated
        condition."""
        return self.error is None and self.permissionship == Permissionship.HAS_PERMISSION

    def __bool__(self) -> bool:
        """Truthiness mirrors has_permission, so `if result:` is safe.

        A CheckResult is an object and would otherwise be unconditionally
        truthy, which would silently grant on a CONDITIONAL_PERMISSION.
        """
        return self.has_permission

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class ResolvedSubject:
    """A subject resolved by `SpiceDBClient.lookup_subjects()` — either the
    matched subject, or (when found in `LookupSubject.excluded_subjects`) a
    subject excluded from a wildcard match."""

    subject_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class LookupSubject:
    """One result from `SpiceDBClient.lookup_subjects()`. When
    `subject.subject_id` is the wildcard "*", `excluded_subjects` lists
    subjects excluded from that wildcard grant — callers MUST treat those
    subjects as NOT having the permission, even though the wildcard would
    otherwise suggest they do."""

    subject: ResolvedSubject
    excluded_subjects: Sequence[ResolvedSubject]
    looked_up_at: ZedToken = ZedToken("")

    def __post_init__(self) -> None:
        _freeze(self)


def _resolved_subject_from_proto(
    v: permission_service_pb2.ResolvedSubject,
) -> ResolvedSubject:
    """Map a proto ResolvedSubject to its native equivalent. Mirrors
    spicedb-go's `resolvedSubjectFromProto` (client/lookup_types.go)."""
    return ResolvedSubject(
        subject_id=v.subject_object_id,
        permissionship=_permissionship_from_proto(v.permissionship),
        partial_caveat=_partial_caveat_from_proto(v),
    )


class DeleteProgress(Enum):
    UNSPECIFIED = 0
    COMPLETE = 1
    PARTIAL = 2


@dataclass(frozen=True)
class DeleteResult:
    revision: ZedToken
    deleted_count: int
    progress: DeleteProgress
    cursor: str | None = None

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class ReadRelationship:
    """One ``read_relationships`` item with its paging state.

    Resume after it with ``read_relationships(cursor=item.after_result_cursor)``
    using the same filter, consistency and page size.
    """

    relationship: Relationship
    after_result_cursor: str | None
    read_at: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class ExportedRelationship:
    """One ``export_relationships`` item with its after-result cursor.

    Export responses carry no revision. Resume with
    ``export_relationships(cursor=item.after_result_cursor)``.
    """

    relationship: Relationship
    after_result_cursor: str | None

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class WriteResult:
    """Result of a relationship transaction, with its committed revision."""

    revision: ZedToken

    def __post_init__(self) -> None:
        _token_string(self.revision)


@dataclass(frozen=True)
class SchemaResult:
    schema: str
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class ExpandResult:
    tree: PermissionTree
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class RelationsResult:
    relations: Sequence[RelationReference]
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SchemaDiffResult:
    diffs: Sequence[SchemaDiff]
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class CountResult:
    count: int | None
    revision: ZedToken | None
    still_calculating: bool = False

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class RoaringLookupResourcesResult:
    bitmap: bytes
    cardinality: int
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)


class PermissionType(Enum):
    UNSPECIFIED = 0
    RELATION = 1
    PERMISSION = 2


class CaveatResult(Enum):
    UNSPECIFIED = 0
    UNEVALUATED = 1
    FALSE = 2
    TRUE = 3
    MISSING_SOME_CONTEXT = 4


@dataclass(frozen=True)
class CaveatEvaluation:
    expression: str
    result: CaveatResult
    context: JSONObject | None
    missing_context: Sequence[str]
    caveat_name: str

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class CheckDebugTrace:
    resource: ObjectRef
    permission: str
    permission_type: PermissionType
    subject: SubjectRef
    result: Permissionship
    caveat_evaluation: CaveatEvaluation | None
    duration: timedelta | None
    was_cached_result: bool | None
    sub_problems: Sequence[CheckDebugTrace] | None
    expiration: datetime | None
    trace_operation_id: str
    source: str

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class DebugInformation:
    check: CheckDebugTrace | None
    schema_used: str

    def __post_init__(self) -> None:
        _freeze(self)


def _safe_enum(cls, value):
    try:
        return cls(value)
    except ValueError:
        return cls(0)


def _debug_trace(p):
    c = p.caveat_evaluation_info
    caveat = (
        CaveatEvaluation(
            c.expression,
            _safe_enum(CaveatResult, c.result),
            context_from_struct(c.context) if c.HasField("context") else None,
            tuple(c.partial_caveat_info.missing_required_context),
            c.caveat_name,
        )
        if p.HasField("caveat_evaluation_info")
        else None
    )
    return CheckDebugTrace(
        ObjectRef._unchecked(p.resource.object_type, p.resource.object_id),
        p.permission,
        _safe_enum(PermissionType, p.permission_type),
        SubjectRef(
            ObjectRef._unchecked(p.subject.object.object_type, p.subject.object.object_id),
            p.subject.optional_relation or None,
        ),
        _check_permissionship_from_proto(p.result),
        caveat,
        p.duration.ToTimedelta() if p.HasField("duration") else None,
        p.was_cached_result if p.HasField("was_cached_result") else None,
        tuple((_debug_trace(x) for x in p.sub_problems.traces))
        if p.HasField("sub_problems")
        else None,
        p.optional_expires_at.ToDatetime(tzinfo=timezone.utc)
        if p.HasField("optional_expires_at")
        else None,
        p.trace_operation_id,
        p.source,
    )


def _debug_information(parent):
    if not parent.HasField("debug_trace"):
        return None
    p = parent.debug_trace
    return DebugInformation(
        _debug_trace(p.check) if p.HasField("check") else None, p.schema_used
    )


@dataclass(frozen=True)
class PermissionSetCursor:
    limit: int = 0
    token: ZedToken | None = None
    starting_index: int = 0
    completed_members: bool = False
    starting_key: str = ""
    cursor: str = ""

    def __post_init__(self) -> None:
        for name in ("limit", "starting_index"):
            value = getattr(self, name)
            if (
                isinstance(value, bool)
                or not isinstance(value, int)
                or not 0 <= value <= 0xFFFFFFFF
            ):
                raise LocalValidationError(f"cursor.{name}: expected uint32")
        _freeze(self)

    def _to_proto(self):
        from authzed.api.materialize.v0 import watchpermissionsets_pb2 as sets

        return sets.Cursor(
            limit=self.limit,
            token=core_pb2.ZedToken(token=_token_string(self.token))
            if self.token is not None
            else None,
            starting_index=self.starting_index,
            completed_members=self.completed_members,
            starting_key=self.starting_key,
            cursor=self.cursor,
        )


@dataclass(frozen=True)
class PermissionSetRef:
    object: ObjectRef
    permission_or_relation: str


@dataclass(frozen=True)
class PermissionSetMember:
    object: ObjectRef
    permission_or_relation: str | None = None


class PermissionSetOperation(Enum):
    UNSPECIFIED = 0
    ADDED = 1
    REMOVED = 2


@dataclass(frozen=True)
class PermissionSetChange:
    revision: ZedToken
    operation: PermissionSetOperation
    parent: PermissionSetRef | None
    child: PermissionSetRef | PermissionSetMember | None


@dataclass(frozen=True)
class PermissionSetCheckpoint:
    revision: ZedToken


@dataclass(frozen=True)
class PermissionSetsLookupRequired:
    revision: ZedToken


@dataclass(frozen=True)
class WatchedPermission:
    resource_type: str
    permission: str
    subject_type: str
    subject_relation: str | None = None


@dataclass(frozen=True)
class PermissionSetSchemaChange:
    revision: ZedToken
    affected_permissions: Sequence[WatchedPermission]

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class UnknownPermissionSetEvent:
    """The server supplied an unknown or absent response variant."""


PermissionSetEvent: TypeAlias = (
    PermissionSetChange
    | PermissionSetCheckpoint
    | PermissionSetsLookupRequired
    | PermissionSetSchemaChange
    | UnknownPermissionSetEvent
)


@dataclass(frozen=True)
class PermissionSetLookup:
    change: PermissionSetChange | None
    cursor: PermissionSetCursor | None


@dataclass(frozen=True)
class PermissionSetFile:
    name: str
    url: str


@dataclass(frozen=True)
class PermissionSetDownload:
    files: Sequence[PermissionSetFile]
    timestamp: datetime | None
    revision: ZedToken

    def __post_init__(self) -> None:
        _freeze(self)
