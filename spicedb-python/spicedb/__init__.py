"""Idiomatic Python client for SpiceDB.

Clients are explicit about their concurrency model -- import the one you want::

    from spicedb.sync import SpiceDBClient   # synchronous
    from spicedb.aio import SpiceDBClient    # asynchronous

Everything else in this package (relationship types, filters, transactions,
consistency constructors, the error hierarchy) is shared by both flavors and
imported from here.
"""

from spicedb.token import ZedToken
from spicedb.options import (
    BulkItemErrors,
    CallOptions,
    DefaultDeadline,
    DeletePartial,
    ResponseMetadata,
    RetryAttempt,
    RetryPolicy,
    Retries,
    StreamRetries,
)
from spicedb.types import (
    ExportedRelationship,
    Precondition,
    PreconditionOperation,
    ReadRelationship,
)
from spicedb.errors import code_of

from spicedb.options import Tracing, LookupPermissionshipFilter, LookupDebug, Wildcards, WatchContent, TransportSecurity, RemoteCredentials
from spicedb.consistency import (
    Consistency,
    ConsistencyMode,
    at_least,
    at_least_or_full,
    at_least_or_min_latency,
    full,
    min_latency,
    snapshot,
)
from spicedb.errors import (
    AlreadyExistsError,
    CancelledError,
    DeadlineExceededError,
    EventLoopBindingError,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    OutOfRangeError,
    PermissionDeniedError,
    ResourceExhaustedError,
    SpiceDBError,
    UnauthenticatedError,
    UnavailableError,
)
from spicedb.types import (
    SchemaTypeReference,
    CheckResult,
    Filter,
    IntermediateNode,
    LeafNode,
    LookupResource,
    LookupSubject,
    ObjectRef,
    PartialCaveatInfo,
    Permissionship,
    PermissionTree,
    ReflectSchemaResult,
    RelationReference,
    Relationship,
    ResolvedSubject,
    SchemaCaveat,
    SchemaCaveatParameter,
    SchemaDefinition,
    SchemaDiff,
    SchemaPermission,
    SchemaRelation,
    SubjectRef,
    Transaction,
    TreeOperation,
    Update,
    UpdateOperation,
    WatchEvent,
)

from spicedb.types import (
    PermissionSetCursor,
    PermissionSetRef,
    PermissionSetMember,
    PermissionSetOperation,
    PermissionSetChange,
    PermissionSetCheckpoint,
    PermissionSetsLookupRequired,
    WatchedPermission,
    PermissionSetSchemaChange,
    UnknownPermissionSetEvent,
    PermissionSetEvent,
    PermissionSetLookup,
    PermissionSetFile,
    PermissionSetDownload,
)

__all__ = [
    # Consistency
    "Consistency",
    "ConsistencyMode",
    "full",
    "min_latency",
    "at_least",
    "at_least_or_full",
    "at_least_or_min_latency",
    "snapshot",
    # Types
    "Relationship",
    "Filter",
    "Transaction",
    "Update",
    "UpdateOperation",
    "WatchEvent",
    "PermissionTree",
    "IntermediateNode",
    "LeafNode",
    "SubjectRef",
    "ObjectRef",
    "TreeOperation",
    # Check results
    "CheckResult",
    # Lookup results
    "LookupResource",
    "LookupSubject",
    "ResolvedSubject",
    "Permissionship",
    "PartialCaveatInfo",
    # Schema reflection / diff
    "ReflectSchemaResult",
    "SchemaDefinition",
    "SchemaRelation",
    "SchemaPermission",
    "SchemaCaveat",
    "SchemaCaveatParameter",
    "SchemaDiff",
    "RelationReference",
    # Errors
    "SpiceDBError",
    "PermissionDeniedError",
    "NotFoundError",
    "AlreadyExistsError",
    "InvalidArgumentError",
    "FailedPreconditionError",
    "UnavailableError",
    "CancelledError",
    "DeadlineExceededError",
    "ResourceExhaustedError",
    "UnauthenticatedError",
    "OutOfRangeError",
    "EventLoopBindingError",
]

from spicedb.types import (
    PermissionCheck,
    Caveat,
    JSONObject,
    JSONValue,
    SubjectFilter,
    SubjectRelationFilter,
    SubjectRelationMode,
    SchemaFilter,
    WriteResult,
    DeleteResult,
    DeleteProgress,
    SchemaResult,
    ExpandResult,
    RelationsResult,
    SchemaDiffResult,
    CountResult,
    RoaringLookupResourcesResult,
    DebugInformation,
    CheckDebugTrace,
    CaveatEvaluation,
    CaveatResult,
    PermissionType,
)
from spicedb._streams import Stream, AsyncStream

__all__ += [
    "PermissionCheck",
    "Caveat",
    "JSONObject",
    "JSONValue",
    "SubjectFilter",
    "SubjectRelationFilter",
    "SubjectRelationMode",
    "SchemaFilter",
    "WriteResult",
    "DeleteResult",
    "DeleteProgress",
    "SchemaResult",
    "ExpandResult",
    "RelationsResult",
    "SchemaDiffResult",
    "CountResult",
    "RoaringLookupResourcesResult",
    "DebugInformation",
    "CheckDebugTrace",
    "CaveatEvaluation",
    "CaveatResult",
    "PermissionType",
    "Stream",
    "AsyncStream",
]


__all__ += ["SchemaTypeReference"]

__all__ += [
    "PermissionSetCursor",
    "PermissionSetRef",
    "PermissionSetMember",
    "PermissionSetOperation",
    "PermissionSetChange",
    "PermissionSetCheckpoint",
    "PermissionSetsLookupRequired",
    "WatchedPermission",
    "PermissionSetSchemaChange",
    "UnknownPermissionSetEvent",
    "PermissionSetEvent",
    "PermissionSetLookup",
    "PermissionSetFile",
    "PermissionSetDownload",
]

__all__ += ["LookupPermissionshipFilter", "ZedToken", "Tracing", "LookupDebug", "Wildcards", "WatchContent", "TransportSecurity", "RemoteCredentials"]

__all__ += [
    "BulkItemErrors",
    "CallOptions",
    "DefaultDeadline",
    "DeletePartial",
    "ResponseMetadata",
    "RetryAttempt",
    "RetryPolicy",
    "Retries",
    "StreamRetries",
    "ExportedRelationship",
    "Precondition",
    "PreconditionOperation",
    "ReadRelationship",
    "code_of",
]
