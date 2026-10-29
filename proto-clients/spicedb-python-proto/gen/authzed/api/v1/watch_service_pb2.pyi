from authzed.api.v1 import core_pb2 as _core_pb2
from authzed.api.v1 import permission_service_pb2 as _permission_service_pb2
from buf.validate import validate_pb2 as _validate_pb2
from google.api import annotations_pb2 as _annotations_pb2
from google.protobuf import struct_pb2 as _struct_pb2
from protoc_gen_openapiv2.options import annotations_pb2 as _annotations_pb2_1
from validate import validate_pb2 as _validate_pb2_1
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class WatchKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    WATCH_KIND_UNSPECIFIED: _ClassVar[WatchKind]
    WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES: _ClassVar[WatchKind]
    WATCH_KIND_INCLUDE_SCHEMA_UPDATES: _ClassVar[WatchKind]
    WATCH_KIND_INCLUDE_CHECKPOINTS: _ClassVar[WatchKind]
WATCH_KIND_UNSPECIFIED: WatchKind
WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES: WatchKind
WATCH_KIND_INCLUDE_SCHEMA_UPDATES: WatchKind
WATCH_KIND_INCLUDE_CHECKPOINTS: WatchKind

class WatchRequest(_message.Message):
    __slots__ = ("optional_object_types", "optional_start_cursor", "optional_relationship_filters", "optional_update_kinds")
    OPTIONAL_OBJECT_TYPES_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_START_CURSOR_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_RELATIONSHIP_FILTERS_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_UPDATE_KINDS_FIELD_NUMBER: _ClassVar[int]
    optional_object_types: _containers.RepeatedScalarFieldContainer[str]
    optional_start_cursor: _core_pb2.ZedToken
    optional_relationship_filters: _containers.RepeatedCompositeFieldContainer[_permission_service_pb2.RelationshipFilter]
    optional_update_kinds: _containers.RepeatedScalarFieldContainer[WatchKind]
    def __init__(self, optional_object_types: _Optional[_Iterable[str]] = ..., optional_start_cursor: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ..., optional_relationship_filters: _Optional[_Iterable[_Union[_permission_service_pb2.RelationshipFilter, _Mapping]]] = ..., optional_update_kinds: _Optional[_Iterable[_Union[WatchKind, str]]] = ...) -> None: ...

class WatchResponse(_message.Message):
    __slots__ = ("updates", "changes_through", "optional_transaction_metadata", "schema_updated", "is_checkpoint", "full_revision_metadata")
    UPDATES_FIELD_NUMBER: _ClassVar[int]
    CHANGES_THROUGH_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_TRANSACTION_METADATA_FIELD_NUMBER: _ClassVar[int]
    SCHEMA_UPDATED_FIELD_NUMBER: _ClassVar[int]
    IS_CHECKPOINT_FIELD_NUMBER: _ClassVar[int]
    FULL_REVISION_METADATA_FIELD_NUMBER: _ClassVar[int]
    updates: _containers.RepeatedCompositeFieldContainer[_core_pb2.RelationshipUpdate]
    changes_through: _core_pb2.ZedToken
    optional_transaction_metadata: _struct_pb2.Struct
    schema_updated: bool
    is_checkpoint: bool
    full_revision_metadata: _containers.RepeatedCompositeFieldContainer[_struct_pb2.Struct]
    def __init__(self, updates: _Optional[_Iterable[_Union[_core_pb2.RelationshipUpdate, _Mapping]]] = ..., changes_through: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ..., optional_transaction_metadata: _Optional[_Union[_struct_pb2.Struct, _Mapping]] = ..., schema_updated: _Optional[bool] = ..., is_checkpoint: _Optional[bool] = ..., full_revision_metadata: _Optional[_Iterable[_Union[_struct_pb2.Struct, _Mapping]]] = ...) -> None: ...
