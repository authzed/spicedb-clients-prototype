from authzed.api.v1 import core_pb2 as _core_pb2
from authzed.api.v1 import permission_service_pb2 as _permission_service_pb2
from buf.validate import validate_pb2 as _validate_pb2
from validate import validate_pb2 as _validate_pb2_1
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ExperimentalCountRelationshipsByFilterRequest(_message.Message):
    __slots__ = ("relationship_filter",)
    RELATIONSHIP_FILTER_FIELD_NUMBER: _ClassVar[int]
    relationship_filter: _permission_service_pb2.RelationshipFilter
    def __init__(self, relationship_filter: _Optional[_Union[_permission_service_pb2.RelationshipFilter, _Mapping]] = ...) -> None: ...

class ExperimentalCountRelationshipsByFilterResponse(_message.Message):
    __slots__ = ("relationship_count", "read_at")
    RELATIONSHIP_COUNT_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    relationship_count: int
    read_at: _core_pb2.ZedToken
    def __init__(self, relationship_count: _Optional[int] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...
