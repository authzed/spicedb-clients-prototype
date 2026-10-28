# frozen_string_literal: true

module SpiceDB
  # Exported native<->protocol conversions, for tools that print or persist
  # protocol-shaped data (JSON for scripts, backup files, debug traces). These
  # are the same conversions the client uses on the wire. This namespace is
  # secondary: ordinary callers never need protocol types.
  #
  # Native -> protocol: object, subject, relationship, filter, precondition,
  # update, consistency, zed_token, check request, and the results
  # check_result, lookup_resource, lookup_subject, expand_result,
  # write_result and read_relationship.
  # Protocol -> native: object, subject, relationship, filter, update,
  # zed_token, check_result (with its debug trace), lookup_resource,
  # lookup_subject, permission_tree and watch_event.
  module ProtoConv
    # The client's own private conversions, hosted on a stateless object.
    class Converter
      include CaveatContext
      include Transport
      include Operations
      include DebugMapping
      include WatchMapping
      include Protocol
      include ResponseMapping
      include Materialize
    end
    private_constant :Converter

    module_function

    def converter
      require 'spicedb_proto'
      @converter ||= Converter.new
    end

    def object_to_proto(object) = converter.send(:object_to_proto, object)
    def object_from_proto(object) = converter.send(:object_from_proto, object)
    def subject_to_proto(subject) = converter.send(:subject_to_proto, subject)
    def subject_from_proto(subject) = converter.send(:subject_ref_from_proto, subject)
    def relationship_to_proto(relationship) = converter.send(:relationship_to_proto, relationship)
    def relationship_from_proto(relationship) = converter.send(:relationship_from_proto, relationship)
    def filter_to_proto(filter) = converter.send(:filter_to_proto, filter)
    def zed_token_to_proto(token) = converter.send(:token_to_proto, token)
    def zed_token_from_proto(token) = ZedToken.from_proto(token)
    def consistency_to_proto(strategy) = converter.send(:build_consistency, strategy)
    def watch_event_from_proto(response) = converter.send(:watch_event_from_proto, response)
    def lookup_resource_from_proto(response) = converter.send(:lookup_resource_from_proto, response, response.after_result_cursor&.token)
    def lookup_subject_from_proto(response) = converter.send(:lookup_subject_from_proto, response)
    def permission_tree_from_proto(tree) = converter.send(:permission_tree_from_proto, tree)

    def precondition_to_proto(precondition) = converter.send(:build_preconditions, [precondition]).first

    def update_to_proto(update)
      Authzed::Api::V1::RelationshipUpdate.new(operation: Protocol::OPERATION_MAP.fetch(update.operation),
                                               relationship: relationship_to_proto(update.relationship))
    end

    def update_from_proto(update) = converter.send(:watch_update_from_proto, update)

    def filter_from_proto(filter)
      subject = filter.optional_subject_filter
      subject &&= SubjectFilter.new(subject_type: subject.subject_type, subject_id: blank(subject.optional_subject_id),
                                    relation: subject_relation_filter(subject.optional_relation))
      Filter.new(resource_type: filter.resource_type, resource_id: blank(filter.optional_resource_id),
                 resource_id_prefix: blank(filter.optional_resource_id_prefix), relation: blank(filter.optional_relation), subject: subject)
    end

    # A CheckPermissionRequest for +check+ (merging call-level +context+).
    def check_request_to_proto(check, consistency:, context: nil, tracing: Tracing::DISABLED)
      Authzed::Api::V1::CheckPermissionRequest.new(**converter.send(:check_request, check, context),
                                                   consistency: consistency_to_proto(consistency),
                                                   with_tracing: Tracing.validate(tracing) == Tracing::ENABLED)
    end

    def check_result_from_proto(response) = converter.send(:check_result, response, ZedToken.from_proto(response.checked_at))

    def check_result_to_proto(result)
      Authzed::Api::V1::CheckPermissionResponse.new(
        checked_at: zed_token_to_proto(result.checked_at),
        permissionship: CHECK_PERMISSIONSHIP.fetch(result.permissionship, :PERMISSIONSHIP_UNSPECIFIED),
        partial_caveat_info: partial_caveat(result.missing_context),
        debug_trace: result.debug_trace && Authzed::Api::V1::DebugInformation.new(
          schema_used: result.debug_trace.schema_used.to_s, check: debug_trace_to_proto(result.debug_trace.check)
        ),
        optional_expires_at: timestamp(result.expiration)
      )
    end

    def debug_trace_to_proto(trace)
      return nil unless trace

      fields = {
        resource: trace.resource && object_to_proto(trace.resource), permission: trace.permission.to_s,
        permission_type: { relation: :PERMISSION_TYPE_RELATION, permission: :PERMISSION_TYPE_PERMISSION }
          .fetch(trace.permission_type, :PERMISSION_TYPE_UNSPECIFIED),
        subject: trace.subject && subject_to_proto(trace.subject),
        result: { no_permission: :PERMISSIONSHIP_NO_PERMISSION, has_permission: :PERMISSIONSHIP_HAS_PERMISSION,
                  conditional_permission: :PERMISSIONSHIP_CONDITIONAL_PERMISSION }.fetch(trace.result, :PERMISSIONSHIP_UNSPECIFIED),
        caveat_evaluation_info: caveat_evaluation_to_proto(trace.caveat_evaluation), duration: duration(trace.duration),
        optional_expires_at: timestamp(trace.expiration), trace_operation_id: trace.trace_operation_id.to_s, source: trace.source.to_s
      }
      case trace.resolution
      when :cached then fields[:was_cached_result] = trace.was_cached_result ? true : false
      when :subproblems
        fields[:sub_problems] = Authzed::Api::V1::CheckDebugTrace::SubProblems.new(traces: trace.subproblems.map { |child| debug_trace_to_proto(child) })
      end
      Authzed::Api::V1::CheckDebugTrace.new(**fields)
    end

    def lookup_resource_to_proto(result)
      Authzed::Api::V1::LookupResourcesResponse.new(
        looked_up_at: zed_token_to_proto(result.looked_up_at), resource_object_id: result.resource_id,
        permissionship: LOOKUP_PERMISSIONSHIP.fetch(result.permissionship, :LOOKUP_PERMISSIONSHIP_UNSPECIFIED),
        partial_caveat_info: result.partial_caveat && partial_caveat(result.partial_caveat.missing_required_context),
        after_result_cursor: result.cursor && Authzed::Api::V1::Cursor.new(token: result.cursor)
      )
    end

    def lookup_subject_to_proto(result)
      Authzed::Api::V1::LookupSubjectsResponse.new(
        looked_up_at: zed_token_to_proto(result.looked_up_at), subject: resolved_subject(result.subject),
        excluded_subjects: result.excluded_subjects.map { |subject| resolved_subject(subject) }
      )
    end

    def expand_result_to_proto(result)
      Authzed::Api::V1::ExpandPermissionTreeResponse.new(expanded_at: zed_token_to_proto(result.revision), tree_root: tree_to_proto(result.tree))
    end

    def write_result_to_proto(result) = Authzed::Api::V1::WriteRelationshipsResponse.new(written_at: zed_token_to_proto(result.revision))

    def read_relationship_to_proto(item)
      Authzed::Api::V1::ReadRelationshipsResponse.new(
        read_at: zed_token_to_proto(item.read_at), relationship: relationship_to_proto(item.relationship),
        after_result_cursor: item.cursor && Authzed::Api::V1::Cursor.new(token: item.cursor)
      )
    end

    CHECK_PERMISSIONSHIP = { no_permission: :PERMISSIONSHIP_NO_PERMISSION, has_permission: :PERMISSIONSHIP_HAS_PERMISSION,
                             conditional_permission: :PERMISSIONSHIP_CONDITIONAL_PERMISSION }.freeze
    LOOKUP_PERMISSIONSHIP = { has_permission: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                              conditional_permission: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION }.freeze
    TREE_OPERATION = { union: :OPERATION_UNION, intersection: :OPERATION_INTERSECTION, exclusion: :OPERATION_EXCLUSION }.freeze
    CAVEAT_RESULT = { unevaluated: :RESULT_UNEVALUATED, evaluated_false: :RESULT_FALSE, evaluated_true: :RESULT_TRUE,
                      missing_context: :RESULT_MISSING_SOME_CONTEXT }.freeze
    private_constant :CHECK_PERMISSIONSHIP, :LOOKUP_PERMISSIONSHIP, :TREE_OPERATION, :CAVEAT_RESULT

    def tree_to_proto(tree)
      return nil unless tree

      fields = { expanded_object: tree.expanded_object && object_to_proto(tree.expanded_object), expanded_relation: tree.expanded_relation.to_s }
      if tree.intermediate
        fields[:intermediate] = Authzed::Api::V1::AlgebraicSubjectSet.new(
          operation: TREE_OPERATION.fetch(tree.intermediate.operation, :OPERATION_UNSPECIFIED),
          children: tree.intermediate.children.map { |child| tree_to_proto(child) }
        )
      elsif tree.leaf
        fields[:leaf] = Authzed::Api::V1::DirectSubjectSet.new(subjects: tree.leaf.subjects.map { |subject| subject_to_proto(subject) })
      end
      Authzed::Api::V1::PermissionRelationshipTree.new(**fields)
    end

    def caveat_evaluation_to_proto(evaluation)
      return nil unless evaluation

      Authzed::Api::V1::CaveatEvalInfo.new(expression: evaluation.expression.to_s,
                                           result: CAVEAT_RESULT.fetch(evaluation.result, :RESULT_UNSPECIFIED),
                                           context: evaluation.context && CaveatContext.check_context_to_struct(evaluation.context),
                                           partial_caveat_info: partial_caveat(evaluation.missing_context), caveat_name: evaluation.caveat_name.to_s)
    end

    def resolved_subject(subject)
      return nil unless subject

      Authzed::Api::V1::ResolvedSubject.new(subject_object_id: subject.subject_id.to_s,
                                            permissionship: LOOKUP_PERMISSIONSHIP.fetch(subject.permissionship, :LOOKUP_PERMISSIONSHIP_UNSPECIFIED),
                                            partial_caveat_info: subject.partial_caveat && partial_caveat(subject.partial_caveat.missing_required_context))
    end

    def partial_caveat(missing)
      return nil if missing.nil? || missing.empty?

      Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: missing.to_a)
    end

    def timestamp(time) = time && Google::Protobuf::Timestamp.new(seconds: time.to_i, nanos: time.nsec)

    def duration(seconds)
      return nil if seconds.nil?

      rational = seconds.to_r
      whole = rational.floor
      Google::Protobuf::Duration.new(seconds: whole, nanos: ((rational - whole) * 1_000_000_000).round)
    end

    def subject_relation_filter(relation)
      return SubjectRelationFilter.any if relation.nil?
      return SubjectRelationFilter.direct if relation.relation.empty?

      SubjectRelationFilter.named(relation.relation)
    end

    def blank(value) = value.nil? || value.empty? ? nil : value
  end
end
