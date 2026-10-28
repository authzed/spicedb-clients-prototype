# frozen_string_literal: true

module SpiceDB
  # Native requests, deterministic streams and one total deadline per operation.
  #
  # Every operation accepts +call_options:+ (a SpiceDB::CallOptions): request
  # metadata, call credentials and response header/trailer capture, applied to
  # every attempt, page and chunk.
  class Client
    include CaveatContext
    include Retrying
    include Transport
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

    attr_reader :proto_client, :retry_policy

    # Operation options shared by every constructor:
    #
    # @param default_timeout [Numeric] seconds of budget for unary operations
    #   called without +timeout:+
    # @param default_deadline [DefaultDeadline] DISABLED runs those operations
    #   unbounded (the caller's own +timeout:+ still applies)
    # @param retry_policy [RetryPolicy] client-wide read retry policy
    # @param retries [Retries] DISABLED turns off every automatic retry
    # @param stream_retries [StreamRetries] DISABLED never re-establishes a
    #   stream, e.g. for watch
    def initialize(endpoint:, token:, transport: TransportSecurity::TLS, remote_credentials: RemoteCredentials::LOOPBACK_ONLY,
                   ca_cert: nil, client_cert: nil, client_key: nil, **)
      configure(**)
      require 'spicedb_proto'
      @proto_client = SpiceDBProto::Client.new(endpoint, token, insecure: TransportSecurity.validate(transport) == TransportSecurity::PLAINTEXT,
                                                                allow_insecure_remote_credentials: RemoteCredentials.validate(remote_credentials) == RemoteCredentials::ALLOW_PLAINTEXT,
                                                                ca_cert: ca_cert, client_cert: client_cert, client_key: client_key)
    rescue SpiceDBProto::InsecureRemoteHostError, ArgumentError => e
      raise InvalidArgumentError, e.message
    end

    # Builds a client over an existing, caller-owned GRPC::Core::Channel (for
    # example one built with custom channel arguments or for a test server).
    # The channel already carries its transport security; +interceptors+ are
    # the place for call credentials on a plaintext channel (for example
    # SpiceDBProto::BearerTokenInterceptor). Operation options apply.
    # +close+ releases this client's streams but never closes the channel.
    def self.from_channel(channel, interceptors: [], **)
      client = allocate
      client.send(:configure, **)
      require 'spicedb_proto'
      client.instance_variable_set(:@proto_client, SpiceDBProto::Client.from_channel(channel, interceptors: interceptors))
      client
    rescue ArgumentError => e
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

    def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, call_options: nil)
      budget = unary_budget(timeout, call_options)
      item = check_request(check, context)
      request = Authzed::Api::V1::CheckPermissionRequest.new(**item,
        consistency: build_consistency(consistency), with_tracing: Tracing.validate(tracing) == Tracing::ENABLED)
      response = rpc(@proto_client.permissions, :check_permission, request, budget)
      check_result(response, ZedToken.from_proto(response.checked_at))
    end

    # With +item_errors: BulkItemErrors::RETURNED+ each failed item's error is
    # on its CheckResult#error (never a grant) instead of failing the call.
    def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED,
                   item_errors: BulkItemErrors::RAISED, call_options: nil)
      tracing = Tracing.validate(tracing)
      return_errors = BulkItemErrors.validate(item_errors) == BulkItemErrors::RETURNED
      budget = unary_budget(timeout, call_options)
      items = checks.to_a.map { |check| Authzed::Api::V1::CheckBulkPermissionsRequestItem.new(**check_request(check, context)) }
      cs = build_consistency(consistency)
      items.each_slice(DEFAULT_CHECK_BATCH_SIZE).with_index.flat_map do |chunk, chunk_index|
        request = Authzed::Api::V1::CheckBulkPermissionsRequest.new(consistency: cs, items: chunk, with_tracing: tracing == Tracing::ENABLED)
        response = rpc(@proto_client.permissions, :check_bulk_permissions, request, budget)
        raise Error, "check_bulk returned #{response.pairs.length} pair(s) for #{chunk.length} request item(s)" unless response.pairs.length == chunk.length

        token = ZedToken.from_proto(response.checked_at)
        response.pairs.each_with_index.map do |pair, index|
          absolute = (chunk_index * DEFAULT_CHECK_BATCH_SIZE) + index
          raise Error, "check item #{absolute}: malformed echoed request" unless pair.request == chunk[index]

          if pair.response == :error
            error = SpiceDB.to_spicedb_error(pair.error)
            error.instance_variable_set(:@input_index, absolute)
            raise error unless return_errors

            next CheckResult.new(permissionship: :unspecified, missing_context: [], checked_at: token, error: error)
          end
          raise Error, "check item #{absolute}: malformed empty pair" unless pair.response == :item

          check_result(pair.item, token)
        end
      end.freeze
    end

    def write(transaction, timeout: nil, metadata: nil, call_options: nil)
      budget = unary_budget(timeout, call_options)
      updates = transaction.updates.map do |item|
        Authzed::Api::V1::RelationshipUpdate.new(operation: OPERATION_MAP.fetch(item.operation),
                                                 relationship: relationship_to_proto(item.relationship))
      end
      request = Authzed::Api::V1::WriteRelationshipsRequest.new(updates: updates,
                                                                optional_preconditions: build_preconditions(transaction.preconditions),
                                                                optional_transaction_metadata: check_context_to_struct(Values.context(metadata)))
      WriteResult.new(revision: ZedToken.from_proto(rpc(@proto_client.permissions, :write_relationships, request, budget, retryable: false).written_at))
    end

    # One bounded request. With the default +partial: DeletePartial::ALLOWED+,
    # +limit+ must be a positive Integer. With +DeletePartial::DISALLOWED+ the
    # delete is all-or-nothing (the server rejects one exceeding its limit) and
    # +limit+ may be nil to use the server's default.
    def delete_relationships(filter, limit: nil, partial: DeletePartial::ALLOWED, must_match: [], must_not_match: [],
                             timeout: nil, cursor: nil, call_options: nil)
      DeletePartial.validate(partial)
      positive(limit, 'limit') unless partial == DeletePartial::DISALLOWED && limit.nil?
      budget = unary_budget(timeout, call_options)
      delete_page(filter, limit, must_match, must_not_match, cursor, budget, partial: partial)
    end

    # Each batch is transactional; the complete operation is non-atomic.
    # Completed batches remain deleted on failure; error.delete_progress
    # carries acknowledged counts and the latest revision. +on_progress+, when
    # given, is called after every acknowledged batch with the cumulative
    # DeleteResult.
    def delete_all_relationships_non_atomic(filter, page_size: 1000, must_match: [], must_not_match: [], timeout: nil, cursor: nil,
                                            on_progress: nil, call_options: nil)
      raise InvalidArgumentError, 'on_progress must respond to call' unless on_progress.nil? || on_progress.respond_to?(:call)

      budget = unary_budget(timeout, call_options)
      positive(page_size, 'page_size')
      progress = DeleteResult.new(revision: nil, deleted_count: 0, progress: :partial, cursor: cursor)
      loop do
        page = delete_page(filter, page_size, must_match, must_not_match, progress.cursor, budget)
        progress = page.with(deleted_count: progress.deleted_count + page.deleted_count)
        on_progress&.call(progress)
        return progress if page.progress == :complete
      end
    rescue StandardError => e
      acknowledged = e.respond_to?(:delete_progress) && e.delete_progress
      progress = acknowledged.with(deleted_count: (progress&.deleted_count || 0) + acknowledged.deleted_count) if acknowledged
      e.instance_variable_set(:@delete_progress, progress)
      raise
    end

    # Yields ReadRelationship items carrying their after-result +cursor+ and
    # +read_at+. To resume, pass an item's cursor as +cursor:+ with the same
    # filter, consistency and page size.
    def read_relationships(filter, consistency:, page_size: 1000, limit: nil, timeout: nil, cursor: nil, call_options: nil, &)
      request = Authzed::Api::V1::ReadRelationshipsRequest.new(consistency: build_consistency(consistency), relationship_filter: filter_to_proto(filter))
      mapper = lambda do |response, after|
        ReadRelationship.new(relationship: relationship_from_proto(response.relationship), cursor: after,
                             read_at: ZedToken.from_proto(response.read_at))
      end
      paged_stream(:permissions, :read_relationships, request, page_size, limit, stream_budget(timeout, call_options), cursor, mapper, &)
    end

    def lookup_resources(resource_type:, permission:, subject:, consistency:, context: nil, limit: nil, # rubocop:disable Metrics/ParameterLists
                         page_size: 1000, timeout: nil, permissionship_filter: LookupPermissionshipFilter::ALL, cursor: nil,
                         call_options: nil, &)
      LookupPermissionshipFilter.validate(permissionship_filter)
      request = Authzed::Api::V1::LookupResourcesRequest.new(consistency: build_consistency(consistency),
                                                             resource_object_type: resource_type, permission: permission, subject: subject_to_proto(subject),
                                                             context: check_context_to_struct(Values.context(context)))
      include_result = (->(result) { result.permissionship == :has_permission } if permissionship_filter == LookupPermissionshipFilter::HAS_PERMISSION_ONLY)
      paged_stream(:permissions, :lookup_resources, request, page_size, limit, stream_budget(timeout, call_options), cursor,
                   method(:lookup_resource_from_proto), include_result: include_result, &)
    end

    def lookup_subjects(resource:, permission:, subject_type:, consistency:, subject_relation: nil,
                        context: nil, timeout: nil, limit: nil, call_options: nil, &)
      request = Authzed::Api::V1::LookupSubjectsRequest.new(consistency: build_consistency(consistency),
                                                            resource: object_to_proto(resource), permission: permission, subject_object_type: subject_type,
                                                            optional_subject_relation: subject_relation || '',
                                                            context: check_context_to_struct(Values.context(context)))
      response_stream(:permissions, :lookup_subjects, request, stream_budget(timeout, call_options), limit: limit,
                                                                                                     mapper: method(:lookup_subject_from_proto), &)
    end

    # Filters are ORed; empty filters watch all relationship changes.
    # Nonempty object_types and filters are mutually exclusive. Include
    # WatchContent::SCHEMA to receive schema-change events
    # (WatchEvent#schema_updated); it combines with CHECKPOINTS.
    def watch(object_types: [], start_revision: nil, content: [WatchContent::RELATIONSHIPS], filters: [], timeout: nil,
              call_options: nil, &)
      response_stream(:watch, :watch, watch_request(object_types, start_revision, content,
                                                    filters: filters), stream_budget(timeout, call_options),
                      retryable: false, mapper: method(:watch_event_from_proto), &)
    end

    def read_schema(timeout: nil, call_options: nil)
      schema, revision = with_budget(timeout, call_options, operation: :read_schema) { |budget| call_read_schema(budget) }
      SchemaResult.new(schema: schema, revision: revision)
    end

    def write_schema(schema, timeout: nil, call_options: nil)
      with_budget(timeout, call_options, retryable: false) { |budget| call_write_schema(schema, budget) }
    end

    def reflect_schema(consistency:, filters: [], timeout: nil, call_options: nil)
      with_budget(timeout, call_options, operation: :reflect_schema) { |budget| call_reflect_schema(consistency, budget, filters) }
    end

    def computable_permissions(definition_name:, relation_name:, consistency:, definition_name_filter: nil, timeout: nil, call_options: nil)
      refs, revision = with_budget(timeout, call_options, operation: :computable_permissions) do |budget|
        call_computable_permissions(consistency, definition_name, relation_name, budget, definition_name_filter)
      end
      RelationsResult.new(relations: refs, revision: revision)
    end

    def dependent_relations(definition_name:, permission_name:, consistency:, timeout: nil, call_options: nil)
      refs, revision = with_budget(timeout, call_options, operation: :dependent_relations) do |budget|
        call_dependent_relations(consistency, definition_name, permission_name, budget)
      end
      RelationsResult.new(relations: refs, revision: revision)
    end

    def diff_schema(comparison_schema, consistency:, timeout: nil, call_options: nil)
      diffs, revision = with_budget(timeout, call_options, operation: :diff_schema) { |budget| call_diff_schema(consistency, comparison_schema, budget) }
      SchemaDiffResult.new(diffs: diffs, revision: revision)
    end

    def expand_permission_tree(resource:, permission:, consistency:, timeout: nil, call_options: nil)
      with_budget(timeout, call_options, operation: :expand_permission_tree) do |budget|
        call_expand_permission_tree(consistency, resource.object_type, resource.object_id, permission, budget)
      end
    end

    # One call is one stream and one server transaction; call again for
    # several transactions. +batch_size+ is relationships per message. When
    # the server aborts the stream, the server's status is raised (for
    # example AlreadyExistsError for a duplicate). Never retried.
    def import_relationships(relationships, timeout: nil, batch_size: DEFAULT_IMPORT_BATCH_SIZE, call_options: nil)
      positive(batch_size, 'batch_size')
      budget = stream_budget(timeout, call_options)
      with_retry(budget: budget, retryable: false) { call_import_relationships(relationships, budget, batch_size) }
    end

    # Yields ExportedRelationship items carrying their response's cursor.
    # Resume by passing it as +cursor:+ with the same filter, consistency and
    # page size.
    def export_relationships(filter = nil, consistency:, page_size: 1000, timeout: nil, limit: nil, cursor: nil, call_options: nil, &)
      request = Authzed::Api::V1::ExportBulkRelationshipsRequest.new(consistency: build_consistency(consistency),
                                                                     optional_relationship_filter: filter && filter_to_proto(filter),
                                                                     optional_cursor: cursor_proto(cursor))
      # Export responses contain batches, unlike the per-item read API.
      export_stream(request, page_size, limit, stream_budget(timeout, call_options), &)
    end

    def experimental_register_relationship_counter(name, filter, timeout: nil, call_options: nil)
      with_budget(timeout, call_options, retryable: false) { |budget| call_register_relationship_counter(name, filter, budget) }
    end

    def experimental_count_relationships(name, timeout: nil, call_options: nil)
      with_budget(timeout, call_options, operation: :experimental_count_relationships) { |budget| call_count_relationships(name, budget) }
    end

    def experimental_unregister_relationship_counter(name, timeout: nil, call_options: nil)
      with_budget(timeout, call_options, retryable: false) { |budget| call_unregister_relationship_counter(name, budget) }
    end

    def experimental_roaring_lookup_resources(resource_type:, permission:, subject:, consistency:, timeout: nil, call_options: nil)
      budget = unary_budget(timeout, call_options)
      request = Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesRequest.new(
        consistency: build_consistency(consistency), resource_object_type: resource_type,
        permission: permission, subject: subject_to_proto(subject)
      )
      response = rpc(@proto_client.materialize, :experimental_roaring_lookup_resources, request, budget)
      RoaringLookupResourcesResult.new(bitmap: response.bitmap, cardinality: response.cardinality,
                                       at_revision: ZedToken.from_proto(response.at_revision))
    end

    private

    def configure(default_timeout: DEFAULT_TIMEOUT_SECONDS, default_deadline: DefaultDeadline::ENABLED, retry_policy: RetryPolicy.new,
                  retries: Retries::ENABLED, stream_retries: StreamRetries::ENABLED)
      Budget.new(default_timeout)
      raise InvalidArgumentError, 'default_timeout is required' if default_timeout.nil?
      raise InvalidArgumentError, 'retry_policy: expected SpiceDB::RetryPolicy' unless retry_policy.is_a?(RetryPolicy)

      @default_timeout = default_timeout
      @default_deadline = DefaultDeadline.validate(default_deadline)
      @retry_policy = retry_policy
      @retries = Retries.validate(retries)
      @stream_retries = StreamRetries.validate(stream_retries)
      @streams = []
      @mutex = Mutex.new
      @closed = false
    end
  end
end
