# frozen_string_literal: true

module SpiceDB
  module SchemaProtocol
    private

    def call_read_schema(timeout_seconds)
      resp = invoke(proto_client.schema, :read_schema,
                    Authzed::Api::V1::ReadSchemaRequest.new,
                    timeout_seconds)
      [resp.schema_text, ZedToken.from_proto(resp.read_at)]
    end

    def call_write_schema(schema, timeout_seconds)
      resp = invoke(proto_client.schema, :write_schema,
                    Authzed::Api::V1::WriteSchemaRequest.new(schema: schema),
                    timeout_seconds)
      ZedToken.from_proto(resp.written_at)
    end

    def call_reflect_schema(consistency, timeout_seconds, filters = [])
      resp = invoke(proto_client.schema, :reflect_schema,
                    Authzed::Api::V1::ReflectSchemaRequest.new(
                      optional_filters: filters.map do |filter|
                        Authzed::Api::V1::ReflectionSchemaFilter.new(**filter.to_h.to_h do |key, value|
                          [:"optional_#{key}_filter", value || '']
                        end)
                      end,
                      consistency: build_consistency(consistency)
                    ),
                    timeout_seconds)

      definitions = resp.definitions.map do |defn|
        relations = defn.relations.map do |rel|
          SchemaRelation.new(
            name: rel.name,
            comment: rel.comment,
            parent_definition_name: rel.parent_definition_name,
            subject_types: rel.subject_types.map do |type|
              SchemaSubjectType.new(definition_name: type.subject_definition_name,
                                    caveat_name: type.optional_caveat_name, kind: type.typeref || :unspecified,
                                    value: type.typeref && type.public_send(type.typeref))
            end
          )
        end
        permissions = defn.permissions.map do |perm|
          SchemaPermission.new(
            name: perm.name,
            comment: perm.comment,
            parent_definition_name: perm.parent_definition_name
          )
        end
        SchemaDefinition.new(
          name: defn.name,
          comment: defn.comment,
          relations: relations,
          permissions: permissions
        )
      end

      ReflectSchemaResult.new(
        definitions: definitions,
        caveats: reflected_caveats(resp.caveats),
        revision: ZedToken.from_proto(resp.read_at)
      )
    end

    def reflected_caveats(caveats)
      caveats.map do |cav|
        params = cav.parameters.map do |param|
          SchemaCaveatParameter.new(
            name: param.name,
            type: param.type,
            parent_caveat_name: param.parent_caveat_name
          )
        end
        SchemaCaveat.new(
          name: cav.name,
          comment: cav.comment,
          expression: cav.expression,
          parameters: params
        )
      end
    end

    def call_computable_permissions(consistency, definition_name, relation_name, timeout_seconds, definition_name_filter = nil)
      resp = invoke(proto_client.schema, :computable_permissions,
                    Authzed::Api::V1::ComputablePermissionsRequest.new(
                      consistency: build_consistency(consistency),
                      definition_name: Values.loose_name(definition_name, 'definition_name'),
                      relation_name: Values.loose_name(relation_name, 'relation_name'),
                      optional_definition_name_filter: Values.loose_name(definition_name_filter, 'definition_name_filter') || ''
                    ),
                    timeout_seconds)

      refs = resp.permissions.map do |perm|
        RelationReference.new(
          definition_name: perm.definition_name,
          relation_name: perm.relation_name,
          is_permission: perm.is_permission
        )
      end

      [refs, ZedToken.from_proto(resp.read_at)]
    end

    def call_dependent_relations(consistency, definition_name, permission_name, timeout_seconds)
      resp = invoke(proto_client.schema, :dependent_relations,
                    Authzed::Api::V1::DependentRelationsRequest.new(
                      consistency: build_consistency(consistency),
                      definition_name: Values.loose_name(definition_name, 'definition_name'),
                      permission_name: Values.loose_name(permission_name, 'permission_name')
                    ),
                    timeout_seconds)

      refs = resp.relations.map do |rel|
        RelationReference.new(
          definition_name: rel.definition_name,
          relation_name: rel.relation_name,
          is_permission: rel.is_permission
        )
      end

      [refs, ZedToken.from_proto(resp.read_at)]
    end

    def call_diff_schema(consistency, comparison_schema, timeout_seconds)
      resp = invoke(proto_client.schema, :diff_schema,
                    Authzed::Api::V1::DiffSchemaRequest.new(
                      consistency: build_consistency(consistency),
                      comparison_schema: comparison_schema
                    ),
                    timeout_seconds)

      diffs = resp.diffs.map { |d| schema_diff_from_proto(d) }
      [diffs, ZedToken.from_proto(resp.read_at)]
    end

    def schema_diff_from_proto(diff)
      kind = diff.diff
      return SchemaDiff.new(kind: 'unknown') unless kind

      payload = diff.public_send(kind)
      fields = { kind: kind.to_s, payload: payload.to_h }
      if kind.to_s.start_with?('definition_')
        fields[:definition_name] = payload.name
      elsif kind.to_s.start_with?('relation_')
        relation = payload.respond_to?(:relation) ? payload.relation : payload
        fields[:definition_name] = relation.parent_definition_name
        fields[:relation_name] = relation.name
      elsif kind.to_s.start_with?('permission_')
        fields[:definition_name] = payload.parent_definition_name
        fields[:permission_name] = payload.name
      elsif kind.to_s.start_with?('caveat_parameter_')
        parameter = payload.respond_to?(:parameter) ? payload.parameter : payload
        fields[:caveat_name] = parameter.parent_caveat_name
      else
        fields[:caveat_name] = payload.name
      end
      SchemaDiff.new(**fields)
    end
  end
end
