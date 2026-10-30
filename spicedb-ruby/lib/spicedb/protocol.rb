# frozen_string_literal: true

module SpiceDB
  # Internal wire conversion and service-specific response mapping.
  module Protocol
    private

    def merge_check_context(call_level, item_level)
      return nil if call_level.nil? && item_level.nil?

      (call_level || {}).merge(item_level || {})
    end

    def require_proto_client!
      return if @proto_client || @connect

      raise SpiceDB::FailedPreconditionError,
            'proto client not available — ensure the spicedb_proto gem is installed and buf-generated stubs exist'
    end

    def build_consistency(strategy)
      Authzed::Api::V1::Consistency.new(**strategy.to_proto)
    end

    def filter_to_proto(filter)
      raise LocalValidationError, 'expected Filter' unless filter.is_a?(Filter)

      subject = filter.subject
      subject_proto = if subject
                        relation = unless subject.relation.kind == :any
                                     Authzed::Api::V1::SubjectFilter::RelationFilter.new(relation: subject.relation.name || '')
                                   end
                        Authzed::Api::V1::SubjectFilter.new(subject_type: subject.subject_type,
                                                            optional_subject_id: subject.subject_id || '', optional_relation: relation)
                      end
      Authzed::Api::V1::RelationshipFilter.new(resource_type: filter.resource_type,
                                               optional_resource_id: filter.resource_id || '', optional_resource_id_prefix: filter.resource_id_prefix || '',
                                               optional_relation: filter.relation || '', optional_subject_filter: subject_proto)
    end

    def relationship_to_proto(rel)
      raise LocalValidationError, 'expected Relationship' unless rel.is_a?(Relationship)

      caveat = rel.caveat && Authzed::Api::V1::ContextualizedCaveat.new(caveat_name: rel.caveat.name,
                                                                        context: caveat_context_to_struct(rel.caveat.context))
      expiration = rel.expiration && Google::Protobuf::Timestamp.new(seconds: rel.expiration.to_i, nanos: rel.expiration.nsec)
      Authzed::Api::V1::Relationship.new(resource: object_to_proto(rel.resource), relation: rel.relation,
                                         subject: subject_to_proto(rel.subject), optional_caveat: caveat,
                                         optional_expires_at: expiration)
    end

    # A response this client cannot interpret (a missing required field, an
    # unset permissionship, a cursor that does not advance, an echo that
    # does not match): INTERNAL, which no default retry policy retries. Never
    # skip such a response or fill its missing fields with empty values. The
    # message says the client detected it, so it is not mistaken for a status
    # the server sent.
    def malformed_response(message)
      Error.new("spicedb: client detected malformed response from server: #{message}", code: GRPC::Core::StatusCodes::INTERNAL)
    end

    # Runs a conversion of server data, reporting a value the native types
    # refuse (an empty name or ID) as a malformed response rather than as the
    # caller's invalid argument.
    def from_server(field)
      yield
    rescue InvalidArgumentError => e
      raise malformed_response("#{field}: #{e.message}")
    end

    def relationship_from_proto(rel, field = 'relationship')
      raise malformed_response("#{field} is missing") if rel.nil?

      caveat = rel.optional_caveat && Caveat.new(name: rel.optional_caveat.caveat_name,
                                                 context: rel.optional_caveat.context && struct_to_caveat_context(rel.optional_caveat.context))
      resource = object_from_proto(rel.resource, "#{field}.resource")
      subject = subject_ref_from_proto(rel.subject, "#{field}.subject")
      from_server(field) do
        Relationship.new(resource: resource, relation: rel.relation, subject: subject, caveat: caveat,
                         expiration: rel.optional_expires_at && Time.at(rel.optional_expires_at.seconds, rel.optional_expires_at.nanos, :nsec))
      end
    end

    def object_from_proto(object, field = 'object')
      raise malformed_response("#{field} is missing") if object.nil?

      from_server(field) { ObjectRef.new(object_type: object.object_type, object_id: object['object_id']) }
    end

    OPERATION_MAP = {
      create: :OPERATION_CREATE,
      touch: :OPERATION_TOUCH,
      delete: :OPERATION_DELETE
    }.freeze

    PRECONDITION_MAP = {
      must_match: :OPERATION_MUST_MATCH,
      must_not_match: :OPERATION_MUST_NOT_MATCH
    }.freeze

    def build_preconditions(preconditions)
      preconditions.map do |pc|
        op = Authzed::Api::V1::Precondition::Operation.const_get(PRECONDITION_MAP.fetch(pc.operation))
        Authzed::Api::V1::Precondition.new(
          operation: op,
          filter: filter_to_proto(pc.filter)
        )
      end
    end

    def call_expand_permission_tree(consistency, resource_type, resource_id, permission, timeout_seconds)
      resp = invoke(proto_client.permissions, :expand_permission_tree,
                    Authzed::Api::V1::ExpandPermissionTreeRequest.new(
                      consistency: build_consistency(consistency),
                      resource: Authzed::Api::V1::ObjectReference.new(
                        object_type: resource_type,
                        object_id: resource_id
                      ),
                      permission: Values.name(permission, 'permission')
                    ),
                    timeout_seconds)

      ExpandResult.new(
        tree: permission_tree_from_proto(resp.tree_root),
        revision: ZedToken.from_proto(resp.expanded_at)
      )
    end

    # One call is one stream and one server transaction. When the server
    # aborts the stream (for example ALREADY_EXISTS for a duplicate), grpc's
    # client streamer reads the call's final status after the failed send and
    # raises it, so the server's status -- not a transport end-of-stream
    # error -- reaches the caller; a send failure that carries no status is
    # replaced by the status the call finished with when one is available.
    def call_import_relationships(relationships, budget, batch_size = Client::DEFAULT_IMPORT_BATCH_SIZE)
      batch = []

      requests = Enumerator.new do |yielder|
        relationships.each do |rel|
          batch << relationship_to_proto(rel)
          next unless batch.size >= batch_size

          yielder << Authzed::Api::V1::ImportBulkRelationshipsRequest.new(
            relationships: batch
          )
          batch = []
        end
        unless batch.empty?
          yielder << Authzed::Api::V1::ImportBulkRelationshipsRequest.new(
            relationships: batch
          )
        end
      end

      owner = make_stream { nil }
      call = proto_client.permissions.import_bulk_relationships(requests, **stub_kwargs(budget), return_op: true)
      owner.attach(call)
      begin
        response = call.execute
        record_metadata(budget, call)
        response.num_loaded
      rescue GRPC::BadStatus => e
        record_metadata(budget, call, e)
        raise
      rescue GRPC::Core::CallError => e
        status = final_status(call)
        raise status if status

        raise e
      end
    ensure
      owner&.close
      relationships.close if relationships.respond_to?(:close)
    end

    def call_register_relationship_counter(name, filter, timeout_seconds)
      invoke(proto_client.experimental, :experimental_register_relationship_counter,
             Authzed::Api::V1::ExperimentalRegisterRelationshipCounterRequest.new(
               name: name,
               relationship_filter: filter_to_proto(filter)
             ),
             timeout_seconds)
    end

    def call_count_relationships(name, timeout_seconds)
      resp = invoke(proto_client.experimental, :experimental_count_relationships,
                    Authzed::Api::V1::ExperimentalCountRelationshipsRequest.new(
                      name: name
                    ),
                    timeout_seconds)

      if resp.counter_still_calculating
        return CountResult.new(
          relationship_count: 0,
          revision: nil,
          still_calculating: true
        )
      end

      cv = resp.read_counter_value
      CountResult.new(
        relationship_count: cv.relationship_count,
        revision: ZedToken.from_proto(cv.read_at),
        still_calculating: false
      )
    end

    def call_unregister_relationship_counter(name, timeout_seconds)
      invoke(proto_client.experimental, :experimental_unregister_relationship_counter,
             Authzed::Api::V1::ExperimentalUnregisterRelationshipCounterRequest.new(
               name: name
             ),
             timeout_seconds)
    end
  end
end
