"""Idiomatic types for SpiceDB relationships, filters, and transactions."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from authzed.api.v1 import core_pb2, permission_service_pb2
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
