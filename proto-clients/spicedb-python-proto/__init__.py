"""SpiceDB Python proto client."""

from authzed.api.v1.core_pb2 import (
    AlgebraicSubjectSet,
    ContextualizedCaveat,
    Cursor,
    DirectSubjectSet,
    ObjectReference,
    PartialCaveatInfo,
    PermissionRelationshipTree,
    Relationship,
    RelationshipUpdate,
    SubjectReference,
    ZedToken,
)
from authzed.api.v1.permission_service_pb2 import (
    CheckBulkPermissionsRequest,
    CheckBulkPermissionsResponse,
    CheckPermissionRequest,
    CheckPermissionResponse,
    DeleteRelationshipsRequest,
    DeleteRelationshipsResponse,
    ExpandPermissionTreeRequest,
    ExpandPermissionTreeResponse,
    ExportBulkRelationshipsRequest,
    ExportBulkRelationshipsResponse,
    ImportBulkRelationshipsRequest,
    ImportBulkRelationshipsResponse,
    LookupResourcesRequest,
    LookupResourcesResponse,
    LookupSubjectsRequest,
    LookupSubjectsResponse,
    ReadRelationshipsRequest,
    ReadRelationshipsResponse,
    WriteRelationshipsRequest,
    WriteRelationshipsResponse,
)
from authzed.api.v1.schema_service_pb2 import (
    ReadSchemaRequest,
    ReadSchemaResponse,
    WriteSchemaRequest,
    WriteSchemaResponse,
)
from authzed.api.v1.watch_service_pb2 import (
    WatchRequest,
    WatchResponse,
)

from client import Client

__all__ = [
    "Client",
    # Core types
    "AlgebraicSubjectSet",
    "ContextualizedCaveat",
    "Cursor",
    "DirectSubjectSet",
    "ObjectReference",
    "PartialCaveatInfo",
    "PermissionRelationshipTree",
    "Relationship",
    "RelationshipUpdate",
    "SubjectReference",
    "ZedToken",
    # Permission service
    "CheckBulkPermissionsRequest",
    "CheckBulkPermissionsResponse",
    "CheckPermissionRequest",
    "CheckPermissionResponse",
    "DeleteRelationshipsRequest",
    "DeleteRelationshipsResponse",
    "ExpandPermissionTreeRequest",
    "ExpandPermissionTreeResponse",
    "ExportBulkRelationshipsRequest",
    "ExportBulkRelationshipsResponse",
    "ImportBulkRelationshipsRequest",
    "ImportBulkRelationshipsResponse",
    "LookupResourcesRequest",
    "LookupResourcesResponse",
    "LookupSubjectsRequest",
    "LookupSubjectsResponse",
    "ReadRelationshipsRequest",
    "ReadRelationshipsResponse",
    "WriteRelationshipsRequest",
    "WriteRelationshipsResponse",
    # Schema service
    "ReadSchemaRequest",
    "ReadSchemaResponse",
    "WriteSchemaRequest",
    "WriteSchemaResponse",
    # Watch service
    "WatchRequest",
    "WatchResponse",
]
