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

    def lookup_resource_from_proto(resp, cursor = nil)
      raise malformed_response('lookup_resources: resource_object_id is missing') if resp.resource_object_id.empty?

      required_permissionship!(resp.permissionship, 'lookup_resources')

      LookupResource.new(
        resource_id: resp.resource_object_id,
        permissionship: permissionship_from_proto(resp.permissionship),
        partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info),
        looked_up_at: ZedToken.from_proto(resp.looked_up_at),
        cursor: cursor
      )
    end

    # Turns one LookupResourcesResponse into its (item, cursor) pairs for
    # +paged_stream+'s +expand:+, whether the server sent the singular,
    # one-resource-per-message form or the batched form (see
    # +response_batch_size:+ on +lookup_resources+). A batched message's
    # results all share its one after_cursor; +lookup_resource_from_proto+
    # reads a batched result the same way it reads the singular response,
    # since both carry resource_object_id, permissionship and
    # partial_caveat_info.
    def lookup_resources_expand(response)
      batch = response.batched
      return [[response, response.after_result_cursor&.token]] unless batch

      raise malformed_response('lookup_resources: response contains no results') if batch.results.empty?

      cursor = batch.after_cursor&.token
      batch.results.map do |result|
        item = Authzed::Api::V1::LookupResourcesResponse.new(looked_up_at: response.looked_up_at, resource_object_id: result.resource_object_id,
                                                             permissionship: result.permissionship, partial_caveat_info: result.partial_caveat_info)
        [item, cursor]
      end
    end

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
