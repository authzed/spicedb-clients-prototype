"""Unit tests for spicedb's native schema reflection/diff types — no SpiceDB
instance needed.

Mirrors the Go reference test coverage implied by spicedb-go/client/schema.go's
mappers (ReflectSchemaResult, SchemaDefinition, SchemaRelation,
SchemaPermission, SchemaCaveat, SchemaCaveatParameter, SchemaDiff,
RelationReference).
"""

from authzed.api.v1 import core_pb2, schema_service_pb2

from spicedb.types import (
    ReflectSchemaResult,
    RelationReference,
    SchemaCaveat,
    SchemaCaveatParameter,
    SchemaDefinition,
    SchemaDiff,
    SchemaPermission,
    SchemaRelation,
    _schema_diff_from_proto,
)


class TestReflectSchemaResultFromProto:
    def test_full_response(self):
        resp = schema_service_pb2.ReflectSchemaResponse(
            definitions=[
                schema_service_pb2.ReflectionDefinition(
                    name="document",
                    comment="a document",
                    relations=[
                        schema_service_pb2.ReflectionRelation(
                            name="viewer",
                            comment="can view",
                            parent_definition_name="document",
                        ),
                    ],
                    permissions=[
                        schema_service_pb2.ReflectionPermission(
                            name="view",
                            comment="view permission",
                            parent_definition_name="document",
                        ),
                    ],
                ),
            ],
            caveats=[
                schema_service_pb2.ReflectionCaveat(
                    name="is_weekday",
                    comment="only on weekdays",
                    expression="day != 'saturday' && day != 'sunday'",
                    parameters=[
                        schema_service_pb2.ReflectionCaveatParameter(
                            name="day",
                            type="string",
                            parent_caveat_name="is_weekday",
                        ),
                    ],
                ),
            ],
            read_at=core_pb2.ZedToken(token="deadbeef"),
        )

        result = ReflectSchemaResult._from_proto(resp)

        assert result.revision == "deadbeef"

        assert result.definitions == [
            SchemaDefinition(
                name="document",
                comment="a document",
                relations=[
                    SchemaRelation(
                        name="viewer",
                        comment="can view",
                        parent_definition_name="document",
                    ),
                ],
                permissions=[
                    SchemaPermission(
                        name="view",
                        comment="view permission",
                        parent_definition_name="document",
                    ),
                ],
            ),
        ]

        assert result.caveats == [
            SchemaCaveat(
                name="is_weekday",
                comment="only on weekdays",
                expression="day != 'saturday' && day != 'sunday'",
                parameters=[
                    SchemaCaveatParameter(
                        name="day",
                        type="string",
                        parent_caveat_name="is_weekday",
                    ),
                ],
            ),
        ]

    def test_empty_response(self):
        resp = schema_service_pb2.ReflectSchemaResponse()
        result = ReflectSchemaResult._from_proto(resp)
        assert result.definitions == []
        assert result.caveats == []
        assert result.revision == ""

    def test_no_proto_leak(self):
        resp = schema_service_pb2.ReflectSchemaResponse(
            definitions=[schema_service_pb2.ReflectionDefinition(name="document")],
        )
        result = ReflectSchemaResult._from_proto(resp)
        assert isinstance(result.definitions[0], SchemaDefinition)
        assert not isinstance(result.definitions[0], schema_service_pb2.ReflectionDefinition)

    def test_frozen(self):
        result = ReflectSchemaResult._from_proto(schema_service_pb2.ReflectSchemaResponse())
        try:
            result.revision = "other"  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass


class TestSchemaDiffFromProto:
    """Mirrors spicedb-go's schemaDiffFromProto switch coverage
    (client/schema.go)."""

    def test_definition_added(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            definition_added=schema_service_pb2.ReflectionDefinition(name="document"),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="definition_added", definition_name="document"
        )

    def test_definition_removed(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            definition_removed=schema_service_pb2.ReflectionDefinition(name="document"),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="definition_removed", definition_name="document"
        )

    def test_relation_added(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            relation_added=schema_service_pb2.ReflectionRelation(
                name="editor", parent_definition_name="document"
            ),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="relation_added",
            definition_name="document",
            relation_name="editor",
        )

    def test_relation_subject_type_added(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            relation_subject_type_added=schema_service_pb2.ReflectionRelationSubjectTypeChange(
                relation=schema_service_pb2.ReflectionRelation(
                    name="viewer", parent_definition_name="document"
                ),
                changed_subject_type=schema_service_pb2.ReflectionTypeReference(
                    subject_definition_name="user"
                ),
            ),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="relation_subject_type_added",
            definition_name="document",
            relation_name="viewer",
        )

    def test_permission_expr_changed(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            permission_expr_changed=schema_service_pb2.ReflectionPermission(
                name="view", parent_definition_name="document"
            ),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="permission_expr_changed",
            definition_name="document",
            permission_name="view",
        )

    def test_caveat_added(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            caveat_added=schema_service_pb2.ReflectionCaveat(name="is_weekday"),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="caveat_added", caveat_name="is_weekday"
        )

    def test_caveat_parameter_type_changed(self):
        d = schema_service_pb2.ReflectionSchemaDiff(
            caveat_parameter_type_changed=schema_service_pb2.ReflectionCaveatParameterTypeChange(
                parameter=schema_service_pb2.ReflectionCaveatParameter(
                    name="day", parent_caveat_name="is_weekday"
                ),
                previous_type="int",
            ),
        )
        assert _schema_diff_from_proto(d) == SchemaDiff(
            kind="caveat_parameter_type_changed", caveat_name="is_weekday"
        )

    def test_unset_diff_is_unknown(self):
        d = schema_service_pb2.ReflectionSchemaDiff()
        assert _schema_diff_from_proto(d) == SchemaDiff(kind="unknown")

    def test_diffs_list_multiple_kinds(self):
        resp = schema_service_pb2.DiffSchemaResponse(
            diffs=[
                schema_service_pb2.ReflectionSchemaDiff(
                    definition_added=schema_service_pb2.ReflectionDefinition(name="document"),
                ),
                schema_service_pb2.ReflectionSchemaDiff(
                    caveat_removed=schema_service_pb2.ReflectionCaveat(name="is_weekday"),
                ),
            ],
            read_at=core_pb2.ZedToken(token="cafebabe"),
        )
        diffs = [_schema_diff_from_proto(d) for d in resp.diffs]
        assert diffs == [
            SchemaDiff(kind="definition_added", definition_name="document"),
            SchemaDiff(kind="caveat_removed", caveat_name="is_weekday"),
        ]

    def test_frozen(self):
        diff = _schema_diff_from_proto(schema_service_pb2.ReflectionSchemaDiff())
        try:
            diff.kind = "other"  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass


class TestRelationReferenceFromProto:
    """Mirrors spicedb-go's RelationReference (client/schema.go), used by
    ComputablePermissions/DependentRelations."""

    def test_from_proto(self):
        proto = schema_service_pb2.ReflectionRelationReference(
            definition_name="document",
            relation_name="view",
            is_permission=True,
        )
        assert RelationReference._from_proto(proto) == RelationReference(
            definition_name="document",
            relation_name="view",
            is_permission=True,
        )

    def test_from_proto_relation_not_permission(self):
        proto = schema_service_pb2.ReflectionRelationReference(
            definition_name="document",
            relation_name="viewer",
            is_permission=False,
        )
        assert RelationReference._from_proto(proto) == RelationReference(
            definition_name="document",
            relation_name="viewer",
            is_permission=False,
        )

    def test_frozen(self):
        ref = RelationReference(
            definition_name="document", relation_name="view", is_permission=True
        )
        try:
            ref.relation_name = "other"  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass
