# frozen_string_literal: true

module SpiceDB
  WriteResult = Values.record(:revision)
  SchemaResult = Values.record(:schema, :revision)
  RelationsResult = Values.record(:relations, :revision)
  SchemaDiffResult = Values.record(:diffs, :revision)
  DeleteResult = Values.record(:revision, :deleted_count, :progress, :cursor)

  SchemaDefinition = Values.record(:name, :comment, :relations, :permissions)
  SchemaSubjectType = Values.record(:definition_name, :caveat_name, :kind, :value)
  SchemaRelation = Values.record(:name, :comment, :parent_definition_name, :subject_types) do
    def initialize(name:, comment:, parent_definition_name:, subject_types: [])
      super(**Values.snapshot({ name: name, comment: comment, parent_definition_name: parent_definition_name, subject_types: subject_types }))
    end
  end
  SchemaPermission = Values.record(:name, :comment, :parent_definition_name)
  SchemaCaveat = Values.record(:name, :comment, :expression, :parameters)
  SchemaCaveatParameter = Values.record(:name, :type, :parent_caveat_name)
  ReflectSchemaResult = Values.record(:definitions, :caveats, :revision)
  RelationReference = Values.record(:definition_name, :relation_name, :is_permission)
  SchemaDiff = Values.record(:kind, :definition_name, :relation_name, :permission_name, :caveat_name, :payload) do
    def initialize(kind:, definition_name: nil, relation_name: nil, permission_name: nil, caveat_name: nil, payload: nil)
      super(**Values.snapshot({ kind: kind, definition_name: definition_name, relation_name: relation_name, permission_name: permission_name,
                                caveat_name: caveat_name, payload: payload }))
    end
  end
  IntermediateNode = Values.record(:operation, :children)
  LeafNode = Values.record(:subjects)
  PermissionTree = Values.record(:expanded_object, :expanded_relation, :intermediate, :leaf)
  ExpandResult = Values.record(:tree, :revision)
  CountResult = Values.record(:relationship_count, :revision, :still_calculating)
  RoaringLookupResourcesResult = Values.record(:bitmap, :cardinality, :at_revision)

  WatchEvent = Values.record(:updates, :changes_through, :is_checkpoint, :schema_updated, :metadata, :full_revision_metadata) do
    def initialize(updates:, changes_through:, is_checkpoint:, schema_updated: false, metadata: nil, full_revision_metadata: [])
      super(**Values.snapshot({ updates: updates, changes_through: changes_through.nil? ? nil : ZedToken.validate(changes_through),
                                is_checkpoint: is_checkpoint,
                                schema_updated: schema_updated, metadata: metadata, full_revision_metadata: full_revision_metadata }))
    end
  end

  PartialCaveatInfo = Values.record(:missing_required_context)
  # +cursor+ resumes the lookup after this item (pass it as +cursor:+ with the
  # same request and page size).
  LookupResource = Values.record(:resource_id, :permissionship, :partial_caveat, :looked_up_at, :cursor) do
    def initialize(resource_id:, permissionship:, partial_caveat:, looked_up_at:, cursor: nil)
      super(**Values.token_fields(Values.snapshot({ resource_id: resource_id, permissionship: permissionship, partial_caveat: partial_caveat,
                                                    looked_up_at: looked_up_at, cursor: cursor })))
    end
  end
  ResolvedSubject = Values.record(:subject_id, :permissionship, :partial_caveat)
  LookupSubject = Values.record(:subject, :excluded_subjects, :looked_up_at)

  # +error+ is set only by check_bulk with +item_errors: BulkItemErrors::RETURNED+;
  # an errored item has :unspecified permissionship and never grants.
  CheckResult = Values.record(:permissionship, :missing_context, :checked_at, :debug_trace, :expiration, :error) do
    def initialize(permissionship:, missing_context:, checked_at:, debug_trace: nil, expiration: nil, error: nil)
      raise LocalValidationError, 'error: expected SpiceDB::Error' unless error.nil? || error.is_a?(Error)

      super(**Values.snapshot({ permissionship: error ? :unspecified : permissionship, missing_context: missing_context,
                                checked_at: checked_at.nil? ? nil : ZedToken.validate(checked_at), debug_trace: debug_trace,
                                expiration: expiration }), error: error)
    end

    def has_permission?
      permissionship == :has_permission
    end
  end
end
