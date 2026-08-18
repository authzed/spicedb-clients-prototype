"""Idiomatic types for SpiceDB relationships, filters, and transactions."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from enum import Enum
from typing import Any

from authzed.api.v1 import core_pb2, permission_service_pb2, schema_service_pb2
from google.protobuf import struct_pb2, timestamp_pb2
from google.protobuf.message import Message

from spicedb.errors import InvalidArgumentError


@dataclass(frozen=True)
class Relationship:
    """An immutable representation of a SpiceDB relationship.

    ``caveat_name``/``caveat_context`` describe a caveat carried BY the
    relationship: they are written to the server (``_to_proto()`` embeds
    them in ``core_pb2.Relationship.optional_caveat``) and evaluated
    whenever anything checks against this stored relationship in the
    future.

    ``check_context`` is a different, check-time-only concept and must not
    be conflated with ``caveat_context``. It has no wire representation on
    ``core_pb2.Relationship`` at all -- ``_to_proto()``/``_from_proto()``
    never read or set it, so it is never written to the server and never
    round-trips through a write. It only matters when this ``Relationship``
    is passed as one of the items to
    ``SpiceDBClient.check_permission()``/``check_permissions()``: there, it
    supplies (or overrides) caveat context for THIS ONE check, merged
    key-by-key with those methods' call-level ``context=`` keyword (this
    item's keys win on conflict; call-level keys the item doesn't mention
    are retained -- see ``spicedb._requests.check_bulk_request``).
    """

    resource_type: str
    resource_id: str
    resource_relation: str
    subject_type: str
    subject_id: str
    subject_relation: str = ""
    caveat_name: str | None = None
    caveat_context: dict[str, Any] | None = None
    expiration: datetime | None = None
    check_context: dict[str, Any] | None = None

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
        check_context: dict[str, Any] | None = None,
    ) -> Relationship:
        """Create a Relationship from 'type:id', 'relation', 'type:id' strings.

        Optionally, the subject string can include a relation as 'type:id#relation'.

        ``check_context`` is check-time-only per-item caveat context -- see
        the class docstring; it is distinct from ``caveat_context``, which is
        written into the relationship at write time.
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
            check_context=check_context,
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
        check_context: dict[str, Any] | None = None,
    ) -> Relationship:
        """Create from 'type:id#relation' and 'type:id' (or 'type:id#relation').

        ``check_context`` is check-time-only per-item caveat context -- see
        the class docstring; it is distinct from ``caveat_context``, which is
        written into the relationship at write time.
        """
        res_ref, relation = resource_and_relation.rsplit("#", 1)
        return cls.from_triple(
            res_ref,
            relation,
            subject,
            caveat_name=caveat_name,
            caveat_context=caveat_context,
            expiration=expiration,
            check_context=check_context,
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
        return Update(
            operation=op, relationship=Relationship._from_proto(u.relationship)
        )


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
    with `watch(include_checkpoints=True)`.
    """

    updates: list[Update]
    changes_through: str
    is_checkpoint: bool = False


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
        """Convert to proto RelationshipFilter.

        Raises ``InvalidArgumentError`` if ``subject_id`` or
        ``subject_relation`` is set without ``subject_type``. The wire's
        ``SubjectFilter.subject_type`` is a required field, so there is no
        way to express a subject ID/relation constraint without it, which
        makes silently dropping the constraint the one unsafe resolution: a
        caller who wrote ``Filter("document", subject_id="alice")``,
        expecting to narrow to alice's relationships, would instead match
        every subject on every document -- e.g. ``delete_relationships``
        would delete every relationship on every document, not just
        alice's. See root DESIGN.md, "RULE: A conversion that cannot
        preserve meaning must fail", clause 1.
        """
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
        elif self.subject_id or self.subject_relation:
            missing = "subject_relation" if not self.subject_id else "subject_id"
            raise InvalidArgumentError(
                f"Filter has {missing} set without subject_type. The wire format "
                "requires subject_type whenever a subject constraint is present -- "
                f"set subject_type before setting {missing}."
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
# SchemaDiff, and RelationReference.


@dataclass(frozen=True)
class SchemaRelation:
    """A relation within a schema definition."""

    name: str
    comment: str
    parent_definition_name: str

    @classmethod
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionRelation
    ) -> "SchemaRelation":
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
    def _from_proto(
        cls, proto: schema_service_pb2.ReflectionPermission
    ) -> "SchemaPermission":
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


def _schema_diff_from_proto(
    proto: schema_service_pb2.ReflectionSchemaDiff,
) -> SchemaDiff:
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


# ── Lookup results ─────────────────────────────────────────────────────
#
# Mirrors spicedb-go's native lookup types and mappers
# (spicedb-go/client/lookup_types.go): Permissionship, PartialCaveatInfo,
# LookupResource, ResolvedSubject, LookupSubject.
#
# Note on the nil-handling difference from Go: Go's mappers take nilable
# pointers (`*v1.PartialCaveatInfo`) and return nil for a nil input. Python
# protobuf message fields are never `None` on attribute access — an unset
# submessage returns a default (zero-value) instance — so
# `_partial_caveat_from_proto` instead takes the *containing* message and
# uses `HasField` to detect presence.


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
    permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION: (
        Permissionship.CONDITIONAL_PERMISSION
    ),
}


def _permissionship_from_proto(v: int) -> Permissionship:
    """Map the proto LookupPermissionship enum to its native equivalent.
    Unrecognized values map to Permissionship.UNSPECIFIED. Mirrors
    spicedb-go's `permissionshipFromProto` (client/lookup_types.go)."""
    return _PERMISSIONSHIP_MAP.get(v, Permissionship.UNSPECIFIED)


_CHECK_PERMISSIONSHIP_MAP = {
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION: (
        Permissionship.HAS_PERMISSION
    ),
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION: (
        Permissionship.CONDITIONAL_PERMISSION
    ),
    permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION: (
        Permissionship.NO_PERMISSION
    ),
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

    missing_required_context: list[str]


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
    partial_caveat: PartialCaveatInfo | None = (
        None  # non-None when Permissionship is Conditional
    )
    # The revision this result was computed at. Identical for every item
    # yielded by a single lookup_resources() call -- it is a property of the
    # call, not of the individual resource. Thread it into at_least() to make
    # a later read observe this lookup (read-your-writes for lookups).
    looked_up_at: str = ""


@dataclass(frozen=True)
class CheckResult:
    """The outcome of a permission check. `permissionship` carries the
    server's three-valued answer — a CONDITIONAL_PERMISSION result means the
    server needed caveat context that was not supplied and is NOT a grant.
    Prefer `has_permission` for the common case.
    """

    permissionship: Permissionship
    # The caveat context keys the server needed and did not receive. Empty
    # unless permissionship is CONDITIONAL_PERMISSION.
    missing_context: list[str]
    # The revision this check was evaluated at. Thread it into at_least() to
    # make a later read observe this check (and everything it observed) --
    # read-your-writes for checks.
    checked_at: str

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


@dataclass(frozen=True)
class ResolvedSubject:
    """A subject resolved by `SpiceDBClient.lookup_subjects()` — either the
    matched subject, or (when found in `LookupSubject.excluded_subjects`) a
    subject excluded from a wildcard match."""

    subject_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None


@dataclass(frozen=True)
class LookupSubject:
    """One result from `SpiceDBClient.lookup_subjects()`. When
    `subject.subject_id` is the wildcard "*", `excluded_subjects` lists
    subjects excluded from that wildcard grant — callers MUST treat those
    subjects as NOT having the permission, even though the wildcard would
    otherwise suggest they do."""

    subject: ResolvedSubject
    excluded_subjects: list[ResolvedSubject]
    # The revision this result was computed at. Identical for every item
    # yielded by a single lookup_subjects() call -- it is a property of the
    # call, not of the individual subject.
    looked_up_at: str = ""


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
