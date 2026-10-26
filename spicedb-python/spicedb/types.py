"""Native immutable request and result values. Transaction is a mutable builder."""

from __future__ import annotations
from spicedb.token import ZedToken, _token_string

from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field, fields
from datetime import datetime, timedelta, timezone
from enum import Enum
from math import isfinite
from types import MappingProxyType
from typing import TypeAlias
from authzed.api.v1 import core_pb2, permission_service_pb2, schema_service_pb2
from google.protobuf import struct_pb2, timestamp_pb2
from google.protobuf.message import Message
from spicedb.errors import InvalidArgumentError

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
            raise InvalidArgumentError(f"{path}: cyclic JSON collection")
        active.add(identity)
        try:
            if isinstance(value, Mapping):
                result = {}
                for key, item in value.items():
                    if not isinstance(key, str):
                        raise InvalidArgumentError(
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
    raise InvalidArgumentError(
        f"{path}: cannot represent {type(value).__name__} as JSON"
    )


def context_to_struct(context: JSONObject, what: str) -> struct_pb2.Struct:
    converted = _json(context, what)
    if not isinstance(converted, dict):
        raise InvalidArgumentError(f"{what}: must be a JSON object")
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


@dataclass(frozen=True)
class ObjectRef:
    object_type: str
    object_id: str

    def _to_proto(self):
        return core_pb2.ObjectReference(
            object_type=self.object_type, object_id=self.object_id
        )

    def __post_init__(self) -> None:
        _freeze(self)


@dataclass(frozen=True)
class SubjectRef:
    object: ObjectRef
    relation: str | None = None

    def __post_init__(self) -> None:
        if self.relation == "":
            raise InvalidArgumentError("subject.relation: must be nonempty or None")
        _freeze(self)

    def _to_proto(self):
        return core_pb2.SubjectReference(
            object=self.object._to_proto(), optional_relation=self.relation or ""
        )


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
            raise InvalidArgumentError(
                "relationship: expected type:id, relation, type:id[#relation]"
            ) from e
        return cls(
            ObjectRef(rt, ri),
            relation,
            SubjectRef(ObjectRef(st, si), sr if sep else None),
            caveat,
            expiration,
        )

    @classmethod
    def from_tuple(
        cls,
        resource_and_relation: str,
        subject: str,
        *,
        caveat: Caveat | None = None,
        expiration: datetime | None = None,
    ) -> Relationship:
        try:
            resource, relation = resource_and_relation.rsplit("#", 1)
        except ValueError as e:
            raise InvalidArgumentError("relationship: expected type:id#relation") from e
        return cls.from_triple(
            resource, relation, subject, caveat=caveat, expiration=expiration
        )

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
            ObjectRef(p.resource.object_type, p.resource.object_id),
            p.relation,
            SubjectRef(
                ObjectRef(p.subject.object.object_type, p.subject.object.object_id),
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
            raise InvalidArgumentError("subject.relation: invalid relation filter")
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


@dataclass(frozen=True)
class SubjectFilter:
    subject_type: str
    subject_id: str | None = field(default=None, kw_only=True)
    relation: SubjectRelationFilter = field(
        default_factory=SubjectRelationFilter.any, kw_only=True
    )

    def __post_init__(self) -> None:
        if not self.subject_type:
            raise InvalidArgumentError("subject.subject_type: required")
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
            raise InvalidArgumentError(
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
    """The kind of mutation represented by an `Update` from `watch()`."""

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


@dataclass(frozen=True)
class Update:
    """A single relationship mutation observed via `SpiceDBClient.watch()`."""

    operation: UpdateOperation
    relationship: Relationship

    @staticmethod
    def _from_proto(u: core_pb2.RelationshipUpdate) -> "Update":
        """Create from a proto RelationshipUpdate."""
        op = _UPDATE_OP_MAP.get(u.operation, UpdateOperation.UNSPECIFIED)
        return Update(
            operation=op, relationship=Relationship._from_proto(u.relationship)
        )

    def __post_init__(self) -> None:
        _freeze(self)


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


@dataclass
class Transaction:
    """A builder for batching relationship writes with preconditions."""

    _updates: list[core_pb2.RelationshipUpdate] = field(
        default_factory=list, init=False
    )
    _preconditions: list[permission_service_pb2.Precondition] = field(
        default_factory=list, init=False
    )

    def create(self, rel: Relationship) -> Transaction:
        """Add a CREATE operation (fails if relationship already exists)."""
        self._updates.append(
            core_pb2.RelationshipUpdate(
                operation=core_pb2.RelationshipUpdate.OPERATION_CREATE,
                relationship=rel._to_proto(),
            )
        )
        return self

    def touch(self, rel: Relationship) -> Transaction:
        """Add a TOUCH operation (create or update)."""
        self._updates.append(
            core_pb2.RelationshipUpdate(
                operation=core_pb2.RelationshipUpdate.OPERATION_TOUCH,
                relationship=rel._to_proto(),
            )
        )
        return self

    def delete(self, rel: Relationship) -> Transaction:
        """Add a DELETE operation."""
        self._updates.append(
            core_pb2.RelationshipUpdate(
                operation=core_pb2.RelationshipUpdate.OPERATION_DELETE,
                relationship=rel._to_proto(),
            )
        )
        return self

    def must_not_match(self, f: Filter) -> Transaction:
        """Add a precondition that no relationships match the filter."""
        self._preconditions.append(
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
                filter=f._to_proto(),
            )
        )
        return self

    def must_match(self, f: Filter) -> Transaction:
        """Add a precondition that at least one relationship matches the filter."""
        self._preconditions.append(
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_MATCH,
                filter=f._to_proto(),
            )
        )
        return self


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
                    object=ObjectRef(s.object.object_type, s.object.object_id),
                    relation=s.optional_relation or None,
                )
                for s in t.leaf.subjects
            ]
        )
    return PermissionTree(
        expanded_object=ObjectRef(
            object_type=t.expanded_object.object_type,
            object_id=t.expanded_object.object_id,
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
        """Create from a proto ReflectionRelationReference."""
        return cls(
            definition_name=proto.definition_name,
            relation_name=proto.relation_name,
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

    @property
    def has_permission(self) -> bool:
        """Whether the subject has the permission outright. False for a
        CONDITIONAL_PERMISSION result -- the server could not evaluate the
        caveat, so treating it as a grant would authorize on an unevaluated
        condition."""
        return self.permissionship == Permissionship.HAS_PERMISSION

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
        ObjectRef(p.resource.object_type, p.resource.object_id),
        p.permission,
        _safe_enum(PermissionType, p.permission_type),
        SubjectRef(
            ObjectRef(p.subject.object.object_type, p.subject.object.object_id),
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
                raise InvalidArgumentError(f"cursor.{name}: expected uint32")
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
