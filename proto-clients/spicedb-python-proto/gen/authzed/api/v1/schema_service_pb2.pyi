from authzed.api.v1 import core_pb2 as _core_pb2
from authzed.api.v1 import permission_service_pb2 as _permission_service_pb2
from buf.validate import validate_pb2 as _validate_pb2
from google.api import annotations_pb2 as _annotations_pb2
from protoc_gen_openapiv2.options import annotations_pb2 as _annotations_pb2_1
from validate import validate_pb2 as _validate_pb2_1
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ReadSchemaRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ReadSchemaResponse(_message.Message):
    __slots__ = ("schema_text", "read_at")
    SCHEMA_TEXT_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    schema_text: str
    read_at: _core_pb2.ZedToken
    def __init__(self, schema_text: _Optional[str] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class WriteSchemaRequest(_message.Message):
    __slots__ = ("schema",)
    SCHEMA_FIELD_NUMBER: _ClassVar[int]
    schema: str
    def __init__(self, schema: _Optional[str] = ...) -> None: ...

class WriteSchemaResponse(_message.Message):
    __slots__ = ("written_at",)
    WRITTEN_AT_FIELD_NUMBER: _ClassVar[int]
    written_at: _core_pb2.ZedToken
    def __init__(self, written_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class ReflectSchemaRequest(_message.Message):
    __slots__ = ("consistency", "optional_filters")
    CONSISTENCY_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_FILTERS_FIELD_NUMBER: _ClassVar[int]
    consistency: _permission_service_pb2.Consistency
    optional_filters: _containers.RepeatedCompositeFieldContainer[ReflectionSchemaFilter]
    def __init__(self, consistency: _Optional[_Union[_permission_service_pb2.Consistency, _Mapping]] = ..., optional_filters: _Optional[_Iterable[_Union[ReflectionSchemaFilter, _Mapping]]] = ...) -> None: ...

class ReflectSchemaResponse(_message.Message):
    __slots__ = ("definitions", "caveats", "read_at")
    DEFINITIONS_FIELD_NUMBER: _ClassVar[int]
    CAVEATS_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    definitions: _containers.RepeatedCompositeFieldContainer[ReflectionDefinition]
    caveats: _containers.RepeatedCompositeFieldContainer[ReflectionCaveat]
    read_at: _core_pb2.ZedToken
    def __init__(self, definitions: _Optional[_Iterable[_Union[ReflectionDefinition, _Mapping]]] = ..., caveats: _Optional[_Iterable[_Union[ReflectionCaveat, _Mapping]]] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class ReflectionSchemaFilter(_message.Message):
    __slots__ = ("optional_definition_name_filter", "optional_caveat_name_filter", "optional_relation_name_filter", "optional_permission_name_filter")
    OPTIONAL_DEFINITION_NAME_FILTER_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_CAVEAT_NAME_FILTER_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_RELATION_NAME_FILTER_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_PERMISSION_NAME_FILTER_FIELD_NUMBER: _ClassVar[int]
    optional_definition_name_filter: str
    optional_caveat_name_filter: str
    optional_relation_name_filter: str
    optional_permission_name_filter: str
    def __init__(self, optional_definition_name_filter: _Optional[str] = ..., optional_caveat_name_filter: _Optional[str] = ..., optional_relation_name_filter: _Optional[str] = ..., optional_permission_name_filter: _Optional[str] = ...) -> None: ...

class ReflectionDefinition(_message.Message):
    __slots__ = ("name", "comment", "relations", "permissions")
    NAME_FIELD_NUMBER: _ClassVar[int]
    COMMENT_FIELD_NUMBER: _ClassVar[int]
    RELATIONS_FIELD_NUMBER: _ClassVar[int]
    PERMISSIONS_FIELD_NUMBER: _ClassVar[int]
    name: str
    comment: str
    relations: _containers.RepeatedCompositeFieldContainer[ReflectionRelation]
    permissions: _containers.RepeatedCompositeFieldContainer[ReflectionPermission]
    def __init__(self, name: _Optional[str] = ..., comment: _Optional[str] = ..., relations: _Optional[_Iterable[_Union[ReflectionRelation, _Mapping]]] = ..., permissions: _Optional[_Iterable[_Union[ReflectionPermission, _Mapping]]] = ...) -> None: ...

class ReflectionCaveat(_message.Message):
    __slots__ = ("name", "comment", "parameters", "expression")
    NAME_FIELD_NUMBER: _ClassVar[int]
    COMMENT_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_FIELD_NUMBER: _ClassVar[int]
    EXPRESSION_FIELD_NUMBER: _ClassVar[int]
    name: str
    comment: str
    parameters: _containers.RepeatedCompositeFieldContainer[ReflectionCaveatParameter]
    expression: str
    def __init__(self, name: _Optional[str] = ..., comment: _Optional[str] = ..., parameters: _Optional[_Iterable[_Union[ReflectionCaveatParameter, _Mapping]]] = ..., expression: _Optional[str] = ...) -> None: ...

class ReflectionCaveatParameter(_message.Message):
    __slots__ = ("name", "type", "parent_caveat_name")
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    PARENT_CAVEAT_NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    type: str
    parent_caveat_name: str
    def __init__(self, name: _Optional[str] = ..., type: _Optional[str] = ..., parent_caveat_name: _Optional[str] = ...) -> None: ...

class ReflectionRelation(_message.Message):
    __slots__ = ("name", "comment", "parent_definition_name", "subject_types")
    NAME_FIELD_NUMBER: _ClassVar[int]
    COMMENT_FIELD_NUMBER: _ClassVar[int]
    PARENT_DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_TYPES_FIELD_NUMBER: _ClassVar[int]
    name: str
    comment: str
    parent_definition_name: str
    subject_types: _containers.RepeatedCompositeFieldContainer[ReflectionTypeReference]
    def __init__(self, name: _Optional[str] = ..., comment: _Optional[str] = ..., parent_definition_name: _Optional[str] = ..., subject_types: _Optional[_Iterable[_Union[ReflectionTypeReference, _Mapping]]] = ...) -> None: ...

class ReflectionTypeReference(_message.Message):
    __slots__ = ("subject_definition_name", "optional_caveat_name", "is_terminal_subject", "optional_relation_name", "is_public_wildcard")
    SUBJECT_DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_CAVEAT_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_TERMINAL_SUBJECT_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_RELATION_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_PUBLIC_WILDCARD_FIELD_NUMBER: _ClassVar[int]
    subject_definition_name: str
    optional_caveat_name: str
    is_terminal_subject: bool
    optional_relation_name: str
    is_public_wildcard: bool
    def __init__(self, subject_definition_name: _Optional[str] = ..., optional_caveat_name: _Optional[str] = ..., is_terminal_subject: _Optional[bool] = ..., optional_relation_name: _Optional[str] = ..., is_public_wildcard: _Optional[bool] = ...) -> None: ...

class ReflectionPermission(_message.Message):
    __slots__ = ("name", "comment", "parent_definition_name")
    NAME_FIELD_NUMBER: _ClassVar[int]
    COMMENT_FIELD_NUMBER: _ClassVar[int]
    PARENT_DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    name: str
    comment: str
    parent_definition_name: str
    def __init__(self, name: _Optional[str] = ..., comment: _Optional[str] = ..., parent_definition_name: _Optional[str] = ...) -> None: ...

class ComputablePermissionsRequest(_message.Message):
    __slots__ = ("consistency", "definition_name", "relation_name", "optional_definition_name_filter")
    CONSISTENCY_FIELD_NUMBER: _ClassVar[int]
    DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    RELATION_NAME_FIELD_NUMBER: _ClassVar[int]
    OPTIONAL_DEFINITION_NAME_FILTER_FIELD_NUMBER: _ClassVar[int]
    consistency: _permission_service_pb2.Consistency
    definition_name: str
    relation_name: str
    optional_definition_name_filter: str
    def __init__(self, consistency: _Optional[_Union[_permission_service_pb2.Consistency, _Mapping]] = ..., definition_name: _Optional[str] = ..., relation_name: _Optional[str] = ..., optional_definition_name_filter: _Optional[str] = ...) -> None: ...

class ReflectionRelationReference(_message.Message):
    __slots__ = ("definition_name", "relation_name", "is_permission")
    DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    RELATION_NAME_FIELD_NUMBER: _ClassVar[int]
    IS_PERMISSION_FIELD_NUMBER: _ClassVar[int]
    definition_name: str
    relation_name: str
    is_permission: bool
    def __init__(self, definition_name: _Optional[str] = ..., relation_name: _Optional[str] = ..., is_permission: _Optional[bool] = ...) -> None: ...

class ComputablePermissionsResponse(_message.Message):
    __slots__ = ("permissions", "read_at")
    PERMISSIONS_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    permissions: _containers.RepeatedCompositeFieldContainer[ReflectionRelationReference]
    read_at: _core_pb2.ZedToken
    def __init__(self, permissions: _Optional[_Iterable[_Union[ReflectionRelationReference, _Mapping]]] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class DependentRelationsRequest(_message.Message):
    __slots__ = ("consistency", "definition_name", "permission_name")
    CONSISTENCY_FIELD_NUMBER: _ClassVar[int]
    DEFINITION_NAME_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_NAME_FIELD_NUMBER: _ClassVar[int]
    consistency: _permission_service_pb2.Consistency
    definition_name: str
    permission_name: str
    def __init__(self, consistency: _Optional[_Union[_permission_service_pb2.Consistency, _Mapping]] = ..., definition_name: _Optional[str] = ..., permission_name: _Optional[str] = ...) -> None: ...

class DependentRelationsResponse(_message.Message):
    __slots__ = ("relations", "read_at")
    RELATIONS_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    relations: _containers.RepeatedCompositeFieldContainer[ReflectionRelationReference]
    read_at: _core_pb2.ZedToken
    def __init__(self, relations: _Optional[_Iterable[_Union[ReflectionRelationReference, _Mapping]]] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class DiffSchemaRequest(_message.Message):
    __slots__ = ("consistency", "comparison_schema")
    CONSISTENCY_FIELD_NUMBER: _ClassVar[int]
    COMPARISON_SCHEMA_FIELD_NUMBER: _ClassVar[int]
    consistency: _permission_service_pb2.Consistency
    comparison_schema: str
    def __init__(self, consistency: _Optional[_Union[_permission_service_pb2.Consistency, _Mapping]] = ..., comparison_schema: _Optional[str] = ...) -> None: ...

class DiffSchemaResponse(_message.Message):
    __slots__ = ("diffs", "read_at")
    DIFFS_FIELD_NUMBER: _ClassVar[int]
    READ_AT_FIELD_NUMBER: _ClassVar[int]
    diffs: _containers.RepeatedCompositeFieldContainer[ReflectionSchemaDiff]
    read_at: _core_pb2.ZedToken
    def __init__(self, diffs: _Optional[_Iterable[_Union[ReflectionSchemaDiff, _Mapping]]] = ..., read_at: _Optional[_Union[_core_pb2.ZedToken, _Mapping]] = ...) -> None: ...

class ReflectionRelationSubjectTypeChange(_message.Message):
    __slots__ = ("relation", "changed_subject_type")
    RELATION_FIELD_NUMBER: _ClassVar[int]
    CHANGED_SUBJECT_TYPE_FIELD_NUMBER: _ClassVar[int]
    relation: ReflectionRelation
    changed_subject_type: ReflectionTypeReference
    def __init__(self, relation: _Optional[_Union[ReflectionRelation, _Mapping]] = ..., changed_subject_type: _Optional[_Union[ReflectionTypeReference, _Mapping]] = ...) -> None: ...

class ReflectionCaveatParameterTypeChange(_message.Message):
    __slots__ = ("parameter", "previous_type")
    PARAMETER_FIELD_NUMBER: _ClassVar[int]
    PREVIOUS_TYPE_FIELD_NUMBER: _ClassVar[int]
    parameter: ReflectionCaveatParameter
    previous_type: str
    def __init__(self, parameter: _Optional[_Union[ReflectionCaveatParameter, _Mapping]] = ..., previous_type: _Optional[str] = ...) -> None: ...

class ReflectionSchemaDiff(_message.Message):
    __slots__ = ("definition_added", "definition_removed", "definition_doc_comment_changed", "relation_added", "relation_removed", "relation_doc_comment_changed", "relation_subject_type_added", "relation_subject_type_removed", "permission_added", "permission_removed", "permission_doc_comment_changed", "permission_expr_changed", "caveat_added", "caveat_removed", "caveat_doc_comment_changed", "caveat_expr_changed", "caveat_parameter_added", "caveat_parameter_removed", "caveat_parameter_type_changed")
    DEFINITION_ADDED_FIELD_NUMBER: _ClassVar[int]
    DEFINITION_REMOVED_FIELD_NUMBER: _ClassVar[int]
    DEFINITION_DOC_COMMENT_CHANGED_FIELD_NUMBER: _ClassVar[int]
    RELATION_ADDED_FIELD_NUMBER: _ClassVar[int]
    RELATION_REMOVED_FIELD_NUMBER: _ClassVar[int]
    RELATION_DOC_COMMENT_CHANGED_FIELD_NUMBER: _ClassVar[int]
    RELATION_SUBJECT_TYPE_ADDED_FIELD_NUMBER: _ClassVar[int]
    RELATION_SUBJECT_TYPE_REMOVED_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_ADDED_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_REMOVED_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_DOC_COMMENT_CHANGED_FIELD_NUMBER: _ClassVar[int]
    PERMISSION_EXPR_CHANGED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_ADDED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_REMOVED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_DOC_COMMENT_CHANGED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_EXPR_CHANGED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_PARAMETER_ADDED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_PARAMETER_REMOVED_FIELD_NUMBER: _ClassVar[int]
    CAVEAT_PARAMETER_TYPE_CHANGED_FIELD_NUMBER: _ClassVar[int]
    definition_added: ReflectionDefinition
    definition_removed: ReflectionDefinition
    definition_doc_comment_changed: ReflectionDefinition
    relation_added: ReflectionRelation
    relation_removed: ReflectionRelation
    relation_doc_comment_changed: ReflectionRelation
    relation_subject_type_added: ReflectionRelationSubjectTypeChange
    relation_subject_type_removed: ReflectionRelationSubjectTypeChange
    permission_added: ReflectionPermission
    permission_removed: ReflectionPermission
    permission_doc_comment_changed: ReflectionPermission
    permission_expr_changed: ReflectionPermission
    caveat_added: ReflectionCaveat
    caveat_removed: ReflectionCaveat
    caveat_doc_comment_changed: ReflectionCaveat
    caveat_expr_changed: ReflectionCaveat
    caveat_parameter_added: ReflectionCaveatParameter
    caveat_parameter_removed: ReflectionCaveatParameter
    caveat_parameter_type_changed: ReflectionCaveatParameterTypeChange
    def __init__(self, definition_added: _Optional[_Union[ReflectionDefinition, _Mapping]] = ..., definition_removed: _Optional[_Union[ReflectionDefinition, _Mapping]] = ..., definition_doc_comment_changed: _Optional[_Union[ReflectionDefinition, _Mapping]] = ..., relation_added: _Optional[_Union[ReflectionRelation, _Mapping]] = ..., relation_removed: _Optional[_Union[ReflectionRelation, _Mapping]] = ..., relation_doc_comment_changed: _Optional[_Union[ReflectionRelation, _Mapping]] = ..., relation_subject_type_added: _Optional[_Union[ReflectionRelationSubjectTypeChange, _Mapping]] = ..., relation_subject_type_removed: _Optional[_Union[ReflectionRelationSubjectTypeChange, _Mapping]] = ..., permission_added: _Optional[_Union[ReflectionPermission, _Mapping]] = ..., permission_removed: _Optional[_Union[ReflectionPermission, _Mapping]] = ..., permission_doc_comment_changed: _Optional[_Union[ReflectionPermission, _Mapping]] = ..., permission_expr_changed: _Optional[_Union[ReflectionPermission, _Mapping]] = ..., caveat_added: _Optional[_Union[ReflectionCaveat, _Mapping]] = ..., caveat_removed: _Optional[_Union[ReflectionCaveat, _Mapping]] = ..., caveat_doc_comment_changed: _Optional[_Union[ReflectionCaveat, _Mapping]] = ..., caveat_expr_changed: _Optional[_Union[ReflectionCaveat, _Mapping]] = ..., caveat_parameter_added: _Optional[_Union[ReflectionCaveatParameter, _Mapping]] = ..., caveat_parameter_removed: _Optional[_Union[ReflectionCaveatParameter, _Mapping]] = ..., caveat_parameter_type_changed: _Optional[_Union[ReflectionCaveatParameterTypeChange, _Mapping]] = ...) -> None: ...
