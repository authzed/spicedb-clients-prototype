# frozen_string_literal: true

module SpiceDB
  PermissionSetCursor = Values.record(:limit, :token, :starting_index, :completed_members, :starting_key, :cursor)
  SetReference = Values.record(:object, :permission_or_relation)
  MemberReference = Values.record(:object, :permission_or_relation)
  PermissionSetChange = Values.record(:revision, :operation, :parent_set, :child_kind, :child_set, :child_member)
  WatchedPermission = Values.record(:resource_type, :permission, :subject_type, :subject_relation)
  PermissionSetEvent = Values.record(:kind, :change, :revision, :affected_permissions)
  PermissionSetLookup = Values.record(:change, :cursor)
  DownloadFile = Values.record(:name, :url)
  DownloadPermissionSetsResult = Values.record(:files, :timestamp, :revision)

  module Materialize
    def experimental_count_relationships_by_filter(filter, timeout: nil)
      budget = unary_budget(timeout)
      request = Authzed::Api::Materialize::V0::ExperimentalCountRelationshipsByFilterRequest.new(relationship_filter: filter_to_proto(filter))
      response = rpc(@proto_client.materialize_relationships, :experimental_count_relationships_by_filter, request, budget)
      CountResult.new(relationship_count: response.relationship_count, revision: ZedToken.from_proto(response.read_at), still_calculating: false)
    end

    def experimental_watch_permission_sets(start_revision: nil, timeout: nil, &)
      request = Authzed::Api::Materialize::V0::WatchPermissionSetsRequest.new(optional_starting_after: token_to_proto(start_revision))
      response_stream(:permission_sets, :watch_permission_sets, request, timeout, retryable: false,
                                                                                  mapper: method(:permission_set_event), &)
    end

    # One bounded lookup exposes the cursor for explicit durable resumption.
    def experimental_lookup_permission_sets(limit: 1000, at_revision: nil, cursor: nil, timeout: nil, &)
      positive(limit, 'limit')
      request = Authzed::Api::Materialize::V0::LookupPermissionSetsRequest.new(limit: limit,
                                                                               optional_at_revision: token_to_proto(at_revision),
                                                                               optional_starting_after_cursor: cursor &&
        Authzed::Api::Materialize::V0::Cursor.new(**cursor.to_h, token: token_to_proto(cursor.token)))
      response_stream(:permission_sets, :lookup_permission_sets, request, timeout, mapper: lambda { |response|
        wire = response.cursor
        PermissionSetLookup.new(change: permission_set_change(response.change), cursor: wire && PermissionSetCursor.new(
          limit: wire.limit, token: ZedToken.from_proto(wire.token), starting_index: wire.starting_index,
          completed_members: wire.completed_members, starting_key: wire.starting_key, cursor: wire.cursor
        ))
      }, &)
    end

    def experimental_download_permission_sets(at_revision: nil, timeout: nil)
      budget = unary_budget(timeout)
      request = Authzed::Api::Materialize::V0::DownloadPermissionSetsRequest.new(optional_at_revision: token_to_proto(at_revision))
      response = rpc(@proto_client.permission_sets, :download_permission_sets, request, budget)
      DownloadPermissionSetsResult.new(files: response.files.map { |file| DownloadFile.new(name: file.name, url: file.url) },
                                       timestamp: response.timestamp && Time.at(response.timestamp.seconds,
                                                                                response.timestamp.nanos, :nsec),
                                       revision: ZedToken.from_proto(response.at_revision))
    end

    private

    def token_to_proto(token) = token.nil? ? nil : Authzed::Api::V1::ZedToken.new(token: ZedToken.validate(token).to_s)

    def permission_set_reference(reference)
      reference && SetReference.new(object: object_from_proto(reference), permission_or_relation: reference.permission_or_relation)
    end

    def permission_set_change(change)
      return nil unless change

      member = change.child_member
      PermissionSetChange.new(revision: ZedToken.from_proto(change.at_revision),
                              operation: { SET_OPERATION_ADDED: :added, SET_OPERATION_REMOVED: :removed }.fetch(change.operation, :unspecified),
                              parent_set: permission_set_reference(change.parent_set), child_kind: change.child || :unspecified,
                              child_set: permission_set_reference(change.child_set), child_member: member && MemberReference.new(
                                object: object_from_proto(member), permission_or_relation: member.optional_permission_or_relation
                              ))
    end

    def permission_set_event(response)
      revision = case response.response
                 when :completed_revision then response.completed_revision
                 when :lookup_permission_sets_required then response.lookup_permission_sets_required.required_lookup_at
                 when :breaking_schema_change then response.breaking_schema_change.change_at
                 end
      affected = response.breaking_schema_change&.affected_permissions || []
      permissions = affected.map do |permission|
        WatchedPermission.new(resource_type: permission.resource_type, permission: permission.permission,
                              subject_type: permission.subject_type, subject_relation: permission.optional_subject_relation)
      end
      PermissionSetEvent.new(kind: response.response || :unspecified, change: permission_set_change(response.change),
                             revision: ZedToken.from_proto(revision), affected_permissions: permissions)
    end
  end
end
