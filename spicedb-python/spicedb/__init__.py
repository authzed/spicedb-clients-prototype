"""Idiomatic Python client for SpiceDB."""

from spicedb.client import SpiceDBClient
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
    InvalidArgumentError,
    NotFoundError,
    PermissionDeniedError,
    SpiceDBError,
)
from spicedb.types import (
    Filter,
    IntermediateNode,
    LeafNode,
    ObjectRef,
    PermissionTree,
    ReflectSchemaResult,
    Relationship,
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
    "SpiceDBClient",
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
    # Schema reflection / diff
    "ReflectSchemaResult",
    "SchemaDefinition",
    "SchemaRelation",
    "SchemaPermission",
    "SchemaCaveat",
    "SchemaCaveatParameter",
    "SchemaDiff",
    # Errors
    "SpiceDBError",
    "PermissionDeniedError",
    "NotFoundError",
    "AlreadyExistsError",
    "InvalidArgumentError",
]
