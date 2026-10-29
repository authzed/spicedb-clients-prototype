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

      LookupResource.new(
        resource_id: resp.resource_object_id,
        permissionship: permissionship_from_proto(resp.permissionship),
        partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info),
        looked_up_at: ZedToken.from_proto(resp.looked_up_at),
        cursor: cursor
      )
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
      if subject.subject_id.nil? || subject.subject_id.empty?
        subject = ResolvedSubject.new(
          subject_id: resp.subject_object_id,
          permissionship: permissionship_from_proto(resp.permissionship),
          partial_caveat: partial_caveat_from_proto(resp.partial_caveat_info)
        )
      end
      raise malformed_response('lookup_subjects: subject is missing') if subject.subject_id.nil? || subject.subject_id.empty?

      LookupSubject.new(
        subject: subject,
        excluded_subjects: excluded_subjects_from_proto(resp),
        looked_up_at: ZedToken.from_proto(resp.looked_up_at)
      )
    end
  end
end
