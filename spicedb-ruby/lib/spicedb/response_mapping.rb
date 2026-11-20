# frozen_string_literal: true

module SpiceDB
  module ResponseMapping
    private

    PERMISSION_TREE_OPERATION_MAP = {
      OPERATION_UNION: :union,
      OPERATION_INTERSECTION: :intersection,
      OPERATION_EXCLUSION: :exclusion
    }.freeze

    def permission_tree_from_proto(t)
      return nil if t.nil?

      expanded_object = t.expanded_object && object_from_proto(t.expanded_object, 'expanded_object')

      intermediate = nil
      leaf = nil

      if (v = t.intermediate)
        intermediate = IntermediateNode.new(
          operation: PERMISSION_TREE_OPERATION_MAP.fetch(v.operation, :unspecified),
          children: v.children.map { |child| permission_tree_from_proto(child) }
        )
      elsif (v = t.leaf)
        leaf = LeafNode.new(
          subjects: v.subjects.map { |subject| subject_ref_from_proto(subject, 'expand leaf subject') }
        )
      end

      PermissionTree.new(
        expanded_object: expanded_object,
        expanded_relation: t.expanded_relation,
        intermediate: intermediate,
        leaf: leaf
      )
    end

    def subject_ref_from_proto(subject, field = 'subject')
      raise malformed_response("#{field} is missing") if subject.nil?

      relation = subject.optional_relation
      object = object_from_proto(subject.object, "#{field}.object")
      from_server(field) { SubjectRef.new(object: object, relation: relation.empty? || relation == '...' ? nil : relation) }
    end

    LOOKUP_PERMISSIONSHIP_MAP = {
      LOOKUP_PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
      LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
    }.freeze

    def permissionship_from_proto(v)
      LOOKUP_PERMISSIONSHIP_MAP.fetch(v, :unspecified)
    end

    CHECK_PERMISSIONSHIP_MAP = {
      PERMISSIONSHIP_NO_PERMISSION: :no_permission,
      PERMISSIONSHIP_HAS_PERMISSION: :has_permission,
      PERMISSIONSHIP_CONDITIONAL_PERMISSION: :conditional_permission
    }.freeze

    def check_permissionship_from_proto(v)
      CHECK_PERMISSIONSHIP_MAP.fetch(v, :unspecified)
    end

    # SpiceDB always sets a check's, a looked-up resource's and a looked-up
    # subject's permissionship,
    # so an unset one there is a malformed response, never an :unspecified
    # result a caller could mistake for a definite answer.
    def required_permissionship!(value, what)
      return unless [:PERMISSIONSHIP_UNSPECIFIED, :LOOKUP_PERMISSIONSHIP_UNSPECIFIED, 0, nil].include?(value)

      raise malformed_response("#{what}: permissionship is unset")
    end

    def partial_caveat_from_proto(v)
      return nil if v.nil?

      PartialCaveatInfo.new(missing_required_context: v.missing_required_context.to_a)
    end

    def missing_context_from_proto(v)
      return [] if v.nil?

      v.missing_required_context.to_a
    end

    def resolved_subject_from_proto(v)
      ResolvedSubject.new(
        subject_id: v&.subject_object_id,
        permissionship: permissionship_from_proto(v&.permissionship),
        partial_caveat: partial_caveat_from_proto(v&.partial_caveat_info)
      )
    end

    # A paged response's resume cursor: the shared cursor of its +batched+
    # field (LookupResources with a response_batch_size) when present, else
    # the response's own singular field. A response without batching (read
    # relationships) never responds to +batched+, so it falls through.
    def response_cursor(response)
      return response.batched.after_cursor&.token if response.respond_to?(:batched) && response.batched

      response.after_result_cursor&.token
    end

    # A paged response's result entries: the contents of +batched+ when
    # present, else the response itself holds its one result's fields.
    def response_entries(response)
      return response.batched.results.to_a if response.respond_to?(:batched) && response.batched

      [response]
    end

    # +entry+ is either the LookupResourcesResponse itself (the common,
    # unbatched form) or one LookupResourcesResult from its +batched+ field;
    # either way +looked_up_at+ comes from +parent+, the response, since a
    # batched result carries no revision of its own.
    def lookup_resource_entry_from_proto(entry, parent, cursor)
      raise malformed_response('lookup_resources: resource_object_id is missing') if entry.resource_object_id.empty?

      required_permissionship!(entry.permissionship, 'lookup_resources')

      LookupResource.new(
        resource_id: entry.resource_object_id,
        permissionship: permissionship_from_proto(entry.permissionship),
        partial_caveat: partial_caveat_from_proto(entry.partial_caveat_info),
        looked_up_at: ZedToken.from_proto(parent.looked_up_at),
        cursor: cursor
      )
    end

    def lookup_resource_from_proto(resp, cursor = nil) = lookup_resource_entry_from_proto(resp, resp, cursor)

    def excluded_subjects_from_proto(resp)
      return resp.excluded_subjects.map { |e| resolved_subject_from_proto(e) } unless resp.excluded_subjects.empty?

      unless resp.excluded_subject_ids.empty?
        return resp.excluded_subject_ids.map do |id|
          ResolvedSubject.new(subject_id: id, permissionship: :unspecified, partial_caveat: nil)
        end
      end

      []
    end

    def lookup_subject_from_proto(resp)
      subject = resolved_subject_from_proto(resp.subject)
      raw_permissionship = resp.subject&.permissionship
      if subject.subject_id.nil? || subject.subject_id.empty?
        raw_permissionship = resp.permissionship
        subject = ResolvedSubject.new(
          subject_id: resp.subject_object_id,
          permissionship: permissionship_from_proto(resp.permissionship),
          partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info)
        )
      end
      raise malformed_response('lookup_subjects: subject is missing') if subject.subject_id.nil? || subject.subject_id.empty?

      # Excluded subjects are left as sent; only the resolved subject's
      # permissionship is required.
      required_permissionship!(raw_permissionship, 'lookup_subjects')

      LookupSubject.new(
        subject: subject,
        excluded_subjects: excluded_subjects_from_proto(resp),
        looked_up_at: ZedToken.from_proto(resp.looked_up_at)
      )
    end
  end
end
