# frozen_string_literal: true

module SpiceDB
  # Native requests, deterministic streams and one total deadline per operation.
  class Client
    include CaveatContext
    include Retrying
    include Operations
    include Materialize
    include DebugMapping
    include WatchMapping
    include Protocol
    include SchemaProtocol
    include ResponseMapping
    extend Connecting

    DEFAULT_READ_PAGE_SIZE = 1000
    DEFAULT_LOOKUP_PAGE_SIZE = 1000
    DEFAULT_EXPORT_PAGE_SIZE = 1000
    DEFAULT_DELETE_PAGE_SIZE = 1000
    DEFAULT_IMPORT_BATCH_SIZE = 1000
    DEFAULT_CHECK_BATCH_SIZE = 1000
    DEFAULT_TIMEOUT_SECONDS = 30

    attr_reader :proto_client

    def initialize(endpoint:, token:, transport: TransportSecurity::TLS, default_timeout: DEFAULT_TIMEOUT_SECONDS,
                   max_retries: 3, remote_credentials: RemoteCredentials::LOOPBACK_ONLY,
                   ca_cert: nil, client_cert: nil, client_key: nil)
      Budget.new(default_timeout)
      raise InvalidArgumentError, 'default_timeout is required' if default_timeout.nil?
      raise InvalidArgumentError, 'max_retries must be nonnegative' unless max_retries.is_a?(Integer) && max_retries >= 0

      @default_timeout = default_timeout
      @max_retries = max_retries
      @streams = []
      @mutex = Mutex.new
      @closed = false
      require 'spicedb_proto'
      @proto_client = SpiceDBProto::Client.new(endpoint, token, insecure: TransportSecurity.validate(transport) == TransportSecurity::PLAINTEXT,
                                                                allow_insecure_remote_credentials: RemoteCredentials.validate(remote_credentials) == RemoteCredentials::ALLOW_PLAINTEXT,
                                                                ca_cert: ca_cert, client_cert: client_cert, client_key: client_key)
    rescue SpiceDBProto::InsecureRemoteHostError, ArgumentError => e
      raise InvalidArgumentError, e.message
    end

    def close
      streams = @mutex.synchronize do
        return if @closed

        @closed = true
        @streams.dup
      end
      streams.each(&:close)
      @proto_client.close
    end

    def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED)
      budget = unary_budget(timeout)
      item = check_request(check, context)
      request = Authzed::Api::V1::CheckPermissionRequest.new(**item,
        consistency: build_consistency(consistency), with_tracing: Tracing.validate(tracing) == Tracing::ENABLED)
      response = rpc(@proto_client.permissions, :check_permission, request, budget)
      check_result(response, ZedToken.from_proto(response.checked_at))
    end

    def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED)
      tracing = Tracing.validate(tracing)
      budget = unary_budget(timeout)
      items = checks.to_a.map { |check| Authzed::Api::V1::CheckBulkPermissionsRequestItem.new(**check_request(check, context)) }
      cs = build_consistency(consistency)
      items.each_slice(DEFAULT_CHECK_BATCH_SIZE).with_index.flat_map do |chunk, chunk_index|
        request = Authzed::Api::V1::CheckBulkPermissionsRequest.new(consistency: cs, items: chunk, with_tracing: Tracing.validate(tracing) == Tracing::ENABLED)
        response = rpc(@proto_client.permissions, :check_bulk_permissions, request, budget)
        raise Error, "check_bulk returned #{response.pairs.length} pair(s) for #{chunk.length} request item(s)" unless response.pairs.length == chunk.length

        response.pairs.each_with_index.map do |pair, index|
          raise Error, "check item #{(chunk_index * DEFAULT_CHECK_BATCH_SIZE) + index}: malformed echoed request" unless pair.request == chunk[index]

          if pair.response == :error
            error = SpiceDB.to_spicedb_error(pair.error)
            error.instance_variable_set(:@input_index, (chunk_index * DEFAULT_CHECK_BATCH_SIZE) + index)
            raise error
          end
          raise Error, "check item #{(chunk_index * DEFAULT_CHECK_BATCH_SIZE) + index}: malformed empty pair" unless pair.response == :item

          check_result(pair.item, ZedToken.from_proto(response.checked_at))
        end
      end.freeze
    end

    def write(transaction, timeout: nil, metadata: nil)
      budget = unary_budget(timeout)
      updates = transaction.updates.map do |item|
        Authzed::Api::V1::RelationshipUpdate.new(operation: OPERATION_MAP.fetch(item[:operation]),
                                                 relationship: relationship_to_proto(item[:relationship]))
      end
      request = Authzed::Api::V1::WriteRelationshipsRequest.new(updates: updates,
                                                                optional_preconditions: build_preconditions(transaction.preconditions),
                                                                optional_transaction_metadata: check_context_to_struct(Values.context(metadata)))
      WriteResult.new(revision: ZedToken.from_proto(rpc(@proto_client.permissions, :write_relationships, request, budget, retryable: false).written_at))
    end

    def delete_relationships(filter, limit:, must_match: [], must_not_match: [], timeout: nil, cursor: nil)
      budget = unary_budget(timeout)
      delete_page(filter, positive(limit, 'limit'), must_match, must_not_match, cursor, budget)
    end

    # Each batch is transactional; the complete operation is non-atomic.
    # Completed batches remain deleted on failure; error.delete_progress
    # carries acknowledged counts and the latest revision.
    def delete_all_relationships_non_atomic(filter, page_size: 1000, must_match: [], must_not_match: [], timeout: nil, cursor: nil)
      budget = unary_budget(timeout)
      positive(page_size, 'page_size')
      progress = DeleteResult.new(revision: nil, deleted_count: 0, progress: :partial, cursor: cursor)
      loop do
        page = delete_page(filter, page_size, must_match, must_not_match, progress.cursor, budget)
        progress = page.with(deleted_count: progress.deleted_count + page.deleted_count)
        return progress if page.progress == :complete
      end
    rescue StandardError => e
      acknowledged = e.respond_to?(:delete_progress) && e.delete_progress
      progress = acknowledged.with(deleted_count: (progress&.deleted_count || 0) + acknowledged.deleted_count) if acknowledged
      e.instance_variable_set(:@delete_progress, progress)
      raise
    end

    def read_relationships(filter, consistency:, page_size: 1000, limit: nil, timeout: nil, &)
      request = Authzed::Api::V1::ReadRelationshipsRequest.new(consistency: build_consistency(consistency), relationship_filter: filter_to_proto(filter))
      paged_stream(:permissions, :read_relationships, request, page_size, limit, timeout,
                   :optional_limit, :optional_cursor, :after_result_cursor, ->(response) { relationship_from_proto(response.relationship) }, &)
    end

    def lookup_resources(resource_type:, permission:, subject:, consistency:, context: nil, limit: nil,
                         page_size: 1000, timeout: nil, permissionship_filter: LookupPermissionshipFilter::ALL, &)
      LookupPermissionshipFilter.validate(permissionship_filter)
      request = Authzed::Api::V1::LookupResourcesRequest.new(consistency: build_consistency(consistency),
                                                             resource_object_type: resource_type, permission: permission, subject: subject_to_proto(subject),
                                                             context: check_context_to_struct(Values.context(context)))
      include_result = (->(result) { result.permissionship == :has_permission } if permissionship_filter == LookupPermissionshipFilter::HAS_PERMISSION_ONLY)
      paged_stream(:permissions, :lookup_resources, request, page_size, limit, timeout,
                   :optional_limit, :optional_cursor, :after_result_cursor, method(:lookup_resource_from_proto),
                   include_result: include_result, &)
    end

    def lookup_subjects(resource:, permission:, subject_type:, consistency:, subject_relation: nil,
                        context: nil, timeout: nil, limit: nil, &)
      request = Authzed::Api::V1::LookupSubjectsRequest.new(consistency: build_consistency(consistency),
                                                            resource: object_to_proto(resource), permission: permission, subject_object_type: subject_type,
                                                            optional_subject_relation: subject_relation || '',
                                                            context: check_context_to_struct(Values.context(context)))
      response_stream(:permissions, :lookup_subjects, request, timeout, limit: limit,
                                                                        mapper: method(:lookup_subject_from_proto), &)
    end

    # Filters are ORed; empty filters watch all relationship changes.
    # Nonempty object_types and filters are mutually exclusive.
    def watch(object_types: [], start_revision: nil, content: [WatchContent::RELATIONSHIPS], filters: [], timeout: nil, &)
      response_stream(:watch, :watch, watch_request(object_types, start_revision, content,
                                                    filters: filters), timeout,
                      retryable: false, mapper: method(:watch_event_from_proto), &)
    end

    def read_schema(timeout: nil)
      schema, revision = with_budget(timeout) { |budget| call_read_schema(budget) }
      SchemaResult.new(schema: schema, revision: revision)
    end

    def write_schema(schema, timeout: nil)
      with_budget(timeout, retryable: false) { |budget| call_write_schema(schema, budget) }
    end

    def reflect_schema(consistency:, filters: [], timeout: nil)
      with_budget(timeout) { |budget| call_reflect_schema(consistency, budget, filters) }
    end

    def computable_permissions(definition_name:, relation_name:, consistency:, definition_name_filter: nil, timeout: nil)
      refs, revision = with_budget(timeout) do |budget|
        call_computable_permissions(consistency, definition_name, relation_name, budget, definition_name_filter)
      end
      RelationsResult.new(relations: refs, revision: revision)
    end

    def dependent_relations(definition_name:, permission_name:, consistency:, timeout: nil)
      refs, revision = with_budget(timeout) { |budget| call_dependent_relations(consistency, definition_name, permission_name, budget) }
      RelationsResult.new(relations: refs, revision: revision)
    end

    def diff_schema(comparison_schema, consistency:, timeout: nil)
      diffs, revision = with_budget(timeout) { |budget| call_diff_schema(consistency, comparison_schema, budget) }
      SchemaDiffResult.new(diffs: diffs, revision: revision)
    end

    def expand_permission_tree(resource:, permission:, consistency:, timeout: nil)
      with_budget(timeout) { |budget| call_expand_permission_tree(consistency, resource.object_type, resource.object_id, permission, budget) }
    end

    def import_relationships(relationships, timeout: nil)
      budget = Budget.new(timeout)
      with_retry(budget: budget, max_retries: 0) { call_import_relationships(relationships, budget) }
    end

    def export_relationships(filter = nil, consistency:, page_size: 1000, timeout: nil, limit: nil, &)
      request = Authzed::Api::V1::ExportBulkRelationshipsRequest.new(consistency: build_consistency(consistency),
                                                                     optional_relationship_filter: filter && filter_to_proto(filter))
      # Export responses contain batches, unlike the per-item read API.
      export_stream(request, page_size, limit, timeout, &)
    end

    def experimental_register_relationship_counter(name, filter, timeout: nil)
      with_budget(timeout, retryable: false) { |budget| call_register_relationship_counter(name, filter, budget) }
    end

    def experimental_count_relationships(name, timeout: nil)
      with_budget(timeout) { |budget| call_count_relationships(name, budget) }
    end

    def experimental_unregister_relationship_counter(name, timeout: nil)
      with_budget(timeout, retryable: false) { |budget| call_unregister_relationship_counter(name, budget) }
    end

    def experimental_roaring_lookup_resources(resource_type:, permission:, subject:, consistency:, timeout: nil)
      budget = unary_budget(timeout)
      request = Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesRequest.new(
        consistency: build_consistency(consistency), resource_object_type: resource_type,
        permission: permission, subject: subject_to_proto(subject)
      )
      response = rpc(@proto_client.materialize, :experimental_roaring_lookup_resources, request, budget)
      RoaringLookupResourcesResult.new(bitmap: response.bitmap, cardinality: response.cardinality,
                                       at_revision: ZedToken.from_proto(response.at_revision))
    end
  end
end
