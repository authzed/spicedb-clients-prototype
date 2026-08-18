"""Idiomatic Python client for SpiceDB.

Clients are explicit about their concurrency model -- import the one you want::

    from spicedb.sync import SpiceDBClient   # synchronous
    from spicedb.aio import SpiceDBClient    # asynchronous

Everything else in this package (relationship types, filters, transactions,
consistency constructors, the error hierarchy) is shared by both flavors and
imported from here.
"""

from spicedb.consistency import (
    Consistency,
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
    EventLoopBindingError,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    PermissionDeniedError,
    SpiceDBError,
    UnavailableError,
)
from spicedb.types import (
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
)

__all__ = [
    # Consistency
    "Consistency",
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
    "PermissionTree",
    "IntermediateNode",
    "LeafNode",
    "SubjectRef",
    "ObjectRef",
    "TreeOperation",
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
    "EventLoopBindingError",
]
