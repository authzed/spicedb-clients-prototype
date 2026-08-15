"""Idiomatic types for SpiceDB relationships, filters, and transactions."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import Any

from authzed.api.v1 import core_pb2, permission_service_pb2, schema_service_pb2
from google.protobuf import struct_pb2, timestamp_pb2


@dataclass(frozen=True)
class Relationship:
    """An immutable representation of a SpiceDB relationship."""

    resource_type: str
    resource_id: str
    resource_relation: str
    subject_type: str
    subject_id: str
    subject_relation: str = ""
    caveat_name: str | None = None
    caveat_context: dict[str, Any] | None = None
    expiration: datetime | None = None

    @classmethod
    def from_triple(
        cls,
        resource: str,
        relation: str,
        subject: str,
        *,
        caveat_name: str | None = None,
        caveat_context: dict[str, Any] | None = None,
        expiration: datetime | None = None,
    ) -> Relationship:
        """Create a Relationship from 'type:id', 'relation', 'type:id' strings.

        Optionally, the subject string can include a relation as 'type:id#relation'.
        """
        res_type, res_id = resource.split(":", 1)
        if "#" in subject:
            subj_ref, subj_rel = subject.rsplit("#", 1)
        else:
            subj_ref = subject
            subj_rel = ""
        subj_type, subj_id = subj_ref.split(":", 1)
        return cls(
            resource_type=res_type,
            resource_id=res_id,
            resource_relation=relation,
            subject_type=subj_type,
            subject_id=subj_id,
            subject_relation=subj_rel,
            caveat_name=caveat_name,
            caveat_context=caveat_context,
            expiration=expiration,
        )

    @classmethod
    def from_tuple(
        cls,
        resource_and_relation: str,
        subject: str,
        *,
        caveat_name: str | None = None,
        caveat_context: dict[str, Any] | None = None,
        expiration: datetime | None = None,
    ) -> Relationship:
        """Create from 'type:id#relation' and 'type:id' (or 'type:id#relation')."""
        res_ref, relation = resource_and_relation.rsplit("#", 1)
        return cls.from_triple(
            res_ref,
            relation,
            subject,
            caveat_name=caveat_name,
            caveat_context=caveat_context,
            expiration=expiration,
        )

    def _to_proto(self) -> core_pb2.Relationship:
        """Convert to proto Relationship."""
        resource = core_pb2.ObjectReference(
            object_type=self.resource_type,
            object_id=self.resource_id,
        )
        subject = core_pb2.SubjectReference(
            object=core_pb2.ObjectReference(
                object_type=self.subject_type,
                object_id=self.subject_id,
            ),
            optional_relation=self.subject_relation,
        )
        caveat = None
        if self.caveat_name is not None:
            ctx = None
            if self.caveat_context is not None:
                ctx = struct_pb2.Struct()
                ctx.update(self.caveat_context)
            caveat = core_pb2.ContextualizedCaveat(
                caveat_name=self.caveat_name,
                context=ctx,
            )
        expires_at = None
        if self.expiration is not None:
            expires_at = timestamp_pb2.Timestamp()
            expires_at.FromDatetime(self.expiration)
        return core_pb2.Relationship(
            resource=resource,
            relation=self.resource_relation,
            subject=subject,
            optional_caveat=caveat,
            optional_expires_at=expires_at,
        )

    @classmethod
    def _from_proto(cls, proto: core_pb2.Relationship) -> Relationship:
        """Create from a proto Relationship."""
        caveat_name = None
        caveat_context = None
        if proto.HasField("optional_caveat") and proto.optional_caveat.caveat_name:
            caveat_name = proto.optional_caveat.caveat_name
            if proto.optional_caveat.HasField("context"):
                caveat_context = dict(proto.optional_caveat.context)
        expiration = None
        if proto.HasField("optional_expires_at"):
            expiration = proto.optional_expires_at.ToDatetime()
        return cls(
            resource_type=proto.resource.object_type,
            resource_id=proto.resource.object_id,
            resource_relation=proto.relation,
            subject_type=proto.subject.object.object_type,
            subject_id=proto.subject.object.object_id,
            subject_relation=proto.subject.optional_relation,
            caveat_name=caveat_name,
            caveat_context=caveat_context,
            expiration=expiration,
        )


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
        return Update(operation=op, relationship=Relationship._from_proto(u.relationship))


@dataclass(frozen=True)
class Filter:
    """A filter for matching relationships."""

    resource_type: str
    resource_id: str = ""
    resource_id_prefix: str = ""
    relation: str = ""
    subject_type: str = ""
    subject_id: str = ""
    subject_relation: str = ""

    def _to_proto(self) -> permission_service_pb2.RelationshipFilter:
        """Convert to proto RelationshipFilter."""
        subject_filter = None
        if self.subject_type:
            rel_filter = None
            if self.subject_relation:
                rel_filter = permission_service_pb2.SubjectFilter.RelationFilter(
                    relation=self.subject_relation,
                )
            subject_filter = permission_service_pb2.SubjectFilter(
                subject_type=self.subject_type,
                optional_subject_id=self.subject_id,
                optional_relation=rel_filter,
            )
        return permission_service_pb2.RelationshipFilter(
            resource_type=self.resource_type,
            optional_resource_id=self.resource_id,
            optional_resource_id_prefix=self.resource_id_prefix,
            optional_relation=self.relation,
            optional_subject_filter=subject_filter,
        )


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
class ObjectRef:
    """Identifies a resource or subject object."""

    object_type: str
    object_id: str


@dataclass(frozen=True)
class SubjectRef:
    """A subject with access at a leaf of a `PermissionTree`."""

    subject_type: str
    subject_id: str
    optional_relation: str = ""


@dataclass(frozen=True)
class IntermediateNode:
    """Combines child subtrees with a set operation."""

    operation: TreeOperation
    children: list["PermissionTree"]


@dataclass(frozen=True)
class LeafNode:
    """Holds the concrete subjects at a leaf of a `PermissionTree`."""

    subjects: list[SubjectRef]


@dataclass(frozen=True)
class PermissionTree:
    """A native node of an expanded permission tree.

    Exactly one of `intermediate` or `leaf` is non-None.
    """

    expanded_object: ObjectRef
    expanded_relation: str
    intermediate: IntermediateNode | None = None
    leaf: LeafNode | None = None


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
                    subject_type=s.object.object_type,
                    subject_id=s.object.object_id,
                    optional_relation=s.optional_relation,
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


# ── Schema reflection / diff ──────────────────────────────────────────
#
# Mirrors spicedb-go's native schema types and mappers
# (spicedb-go/client/schema.go): ReflectSchemaResult, SchemaDefinition,
# SchemaRelation, SchemaPermission, SchemaCaveat, SchemaCaveatParameter,
# and SchemaDiff. (spicedb-go's RelationReference type, used by
# ComputablePermissions/DependentRelations, has no Python counterpart yet —
# those two methods are not implemented in this client.)


@dataclass(frozen=True)
class SchemaRelation:
    """A relation within a schema definition."""

    name: str
    comment: str
    parent_definition_name: str

    @classmethod
    def _from_proto(cls, proto: schema_service_pb2.ReflectionRelation) -> "SchemaRelation":
        """Create from a proto ReflectionRelation."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            parent_definition_name=proto.parent_definition_name,
        )


@dataclass(frozen=True)
class SchemaPermission:
    """A permission within a schema definition."""

    name: str
    comment: str
    parent_definition_name: str

    @classmethod
    def _from_proto(cls, proto: schema_service_pb2.ReflectionPermission) -> "SchemaPermission":
        """Create from a proto ReflectionPermission."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            parent_definition_name=proto.parent_definition_name,
        )


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


@dataclass(frozen=True)
class SchemaDefinition:
    """A definition in a SpiceDB schema, including its relations and
    permissions."""

    name: str
    comment: str
    relations: list[SchemaRelation]
    permissions: list[SchemaPermission]

    @classmethod
    def _from_proto(cls, proto: schema_service_pb2.ReflectionDefinition) -> "SchemaDefinition":
        """Create from a proto ReflectionDefinition."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            relations=[SchemaRelation._from_proto(r) for r in proto.relations],
            permissions=[SchemaPermission._from_proto(p) for p in proto.permissions],
        )


@dataclass(frozen=True)
class SchemaCaveat:
    """A caveat defined in a SpiceDB schema."""

    name: str
    comment: str
    expression: str
    parameters: list[SchemaCaveatParameter]

    @classmethod
    def _from_proto(cls, proto: schema_service_pb2.ReflectionCaveat) -> "SchemaCaveat":
        """Create from a proto ReflectionCaveat."""
        return cls(
            name=proto.name,
            comment=proto.comment,
            expression=proto.expression,
            parameters=[SchemaCaveatParameter._from_proto(p) for p in proto.parameters],
        )


@dataclass(frozen=True)
class ReflectSchemaResult:
    """The result of a schema reflection call."""

    definitions: list[SchemaDefinition]
    caveats: list[SchemaCaveat]
    revision: str

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectSchemaResponse
    ) -> "ReflectSchemaResult":
        """Create from a proto ReflectSchemaResponse."""
        return cls(
            definitions=[SchemaDefinition._from_proto(d) for d in proto.definitions],
            caveats=[SchemaCaveat._from_proto(c) for c in proto.caveats],
            revision=proto.read_at.token,
        )


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


def _schema_diff_from_proto(proto: schema_service_pb2.ReflectionSchemaDiff) -> SchemaDiff:
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
