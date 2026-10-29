from authzed.api.v1 import core_pb2 as _core_pb2
from authzed.api.v1 import permission_service_pb2 as _permission_service_pb2
from buf.validate import validate_pb2 as _validate_pb2
from validate import validate_pb2 as _validate_pb2_1
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ExperimentalRoaringLookupResourcesRequest(_message.Message):
    __slots__ = ("consistency", "resource_object_type", "permission", "subject")
    CONSISTENCY_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_OBJECT_TYPE_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    consistency: _permission_service_pb2.Consistency
    resource_object_type: str
    permission: str
    subject: _core_pb2.SubjectReference
    def __init__(self, consistency: _Optional[_Union[_permission_service_pb2.Consistency, _Mapping]] = ..., resource_object_type: _Optional[str] = ..., permission: _Optional[str] = ..., subject: _Optional[_Union[_core_pb2.SubjectReference, _Mapping]] = ...) -> None: ...

class ExperimentalRoaringLookupResourcesResponse(_message.Message):
    __slots__ = ("bitmap", "cardinality", "at_revision")
    BITMAP_FIELD_NUMBER: _ClassVar[int]
    CARDINALITY_FIELD_NUMBER: _ClassVar[int]
    AT_REVISION_FIELD_NUMBER: _ClassVar[int]
    bitmap: bytes
    cardinality: int
    at_revision: _core_pb2.ZedToken
    def __init__(self, bitmap: _Optional[bytes] = ..., cardinality: _Optional[int] = ..., at_revision: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...
