# frozen_string_literal: true

module MaterializeSetStandIns
  V0 = Authzed::Api::Materialize::V0
  V1 = Authzed::Api::V1

  def self.authenticate(call)
    raise GRPC::Unauthenticated, 'missing bearer token' unless call.metadata['authorization'] == 'Bearer materialize-token'
  end

  class Counts < V0::RelationshipsService::Service
    def experimental_count_relationships_by_filter(request, call)
      MaterializeSetStandIns.authenticate(call)
      raise GRPC::InvalidArgument, 'wrong filter' unless request.relationship_filter.resource_type == 'document'

      V0::ExperimentalCountRelationshipsByFilterResponse.new(relationship_count: 12, read_at: V1::ZedToken.new(token: 'count-rev'))
    end
  end

  class Sets < V0::WatchPermissionSetsService::Service
    def watch_permission_sets(_request, call)
      MaterializeSetStandIns.authenticate(call)
      [V0::WatchPermissionSetsResponse.new(completed_revision: V1::ZedToken.new(token: 'checkpoint'))]
    end

    def lookup_permission_sets(request, call)
      MaterializeSetStandIns.authenticate(call)
      raise GRPC::InvalidArgument, 'wrong limit' unless request.limit == 2

      [V0::LookupPermissionSetsResponse.new(
        change: V0::PermissionSetChange.new(at_revision: V1::ZedToken.new(token: 'set-rev'), operation: :SET_OPERATION_ADDED,
                                            parent_set: V0::SetReference.new(object_type: 'document', object_id: 'one', permission_or_relation: 'view'),
                                            child_member: V0::MemberReference.new(object_type: 'user', object_id: 'alice')),
        cursor: V0::Cursor.new(limit: 2, token: V1::ZedToken.new(token: 'set-rev'), starting_index: 7,
                               completed_members: false, starting_key: 'key', cursor: 'opaque')
      )]
    end

    def download_permission_sets(_request, call)
      MaterializeSetStandIns.authenticate(call)
      V0::DownloadPermissionSetsResponse.new(files: [V0::File.new(name: 'snapshot', url: 'https://example.invalid/snapshot')],
                                             timestamp: Google::Protobuf::Timestamp.new(seconds: 12, nanos: 34),
                                             at_revision: V1::ZedToken.new(token: 'download-rev'))
    end
  end
end
