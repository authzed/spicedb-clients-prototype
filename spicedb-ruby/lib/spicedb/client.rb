# frozen_string_literal: true

module SpiceDB
  # Native requests, deterministic streams and one total deadline per operation.
  #
  # One client is safe for concurrent use from many threads: share one per
  # process (a Rails initializer constant, for example) rather than building
  # one per request. Each operation is an independent gRPC call on one
  # channel, and the client's own bookkeeping (open streams, the lazily
  # opened connection) is guarded by a Mutex. A Stream returned by a
  # streaming operation belongs to the thread that iterates it.
  #
  # The client opens its gRPC channel on first use, in the process making
  # that call. A client built before a fork (Puma or Unicorn with
  # +preload_app!+) therefore works in every forked worker, as long as the
  # parent made no call with it. gRPC cannot carry a connection across a
  # fork, so a call in a child process whose client was already connected in
  # the parent raises ForkedClientError naming the fix, unless grpc's own fork
  # support is enabled (GRPC_ENABLE_FORK_SUPPORT=1 on Linux, with
  # GRPC.prefork and GRPC.postfork_child around the fork). See README.md,
  # "Concurrency and forking".
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
    DEFAULT_CONNECT_TIMEOUT_SECONDS = 10

    # Every operation's name, as accepted by +operation_retry_policies:+.
    OPERATIONS = %i[
      check check_bulk write delete_relationships delete_all_relationships_non_atomic read_relationships lookup_resources
      lookup_subjects watch read_schema write_schema reflect_schema computable_permissions dependent_relations diff_schema
      expand_permission_tree import_relationships export_relationships experimental_register_relationship_counter
      experimental_count_relationships experimental_unregister_relationship_counter experimental_roaring_lookup_resources
      experimental_count_relationships_by_filter experimental_watch_permission_sets experimental_lookup_permission_sets
      experimental_download_permission_sets
    ].freeze

    # Operations that are never idempotent on replay; +operation_retry_policies:+
    # accepts only a policy without retries for them.
    NEVER_IDEMPOTENT_OPERATIONS = %i[
      delete_relationships delete_all_relationships_non_atomic import_relationships
      experimental_register_relationship_counter experimental_unregister_relationship_counter
    ].freeze

    attr_reader :retry_policy, :operation_retry_policies

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
    # @param operation_retry_policies [Hash{Symbol => RetryPolicy}] the retry
    #   policy for every call of one operation (keys from OPERATIONS), e.g.
    #   +{ watch: RetryPolicy.new(max_retries: 0) }+. A call's own
    #   +retry_policy:+ wins over it, and it wins over the client-wide policy
    #   (including stream establishment and Retries/StreamRetries::DISABLED).
    #   For +:write+ it retries only idempotent transactions
    #   (Transaction#idempotent?) and leaves others unretried; operations in
    #   NEVER_IDEMPOTENT_OPERATIONS reject a policy with retries.
    def initialize(endpoint:, token:, transport: TransportSecurity::TLS, remote_credentials: RemoteCredentials::LOOPBACK_ONLY,
                   ca_cert: nil, client_cert: nil, client_key: nil, **)
      configure(**)
      options = { insecure: TransportSecurity.validate(transport) == TransportSecurity::PLAINTEXT,
                  allow_insecure_remote_credentials: RemoteCredentials.validate(remote_credentials) == RemoteCredentials::ALLOW_PLAINTEXT,
                  ca_cert: ca_cert, client_cert: client_cert, client_key: client_key }
      # Every construction refusal runs now; the channel opens on first use.
      SpiceDBProto::Client.validate_connection!(endpoint, **options)
      @connect = -> { SpiceDBProto::Client.new(endpoint, token, **options) }
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
      client.send(:adopt_connection, SpiceDBProto::Client.from_channel(channel, interceptors: interceptors))
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
      # A connection opened in another process (before a fork) belongs to it.
      @proto_client&.close if @connected_pid.nil? || @connected_pid == Process.pid
    end

    # The protocol client (a SpiceDBProto::Client) over this client's
    # authenticated connection: a secondary escape hatch. The connection
    # opens on first use (this call or any operation) in the calling process.
    #
    # @raise [ForkedClientError] the connection was opened in the process
    #   this one forked from (see the class documentation)
    def proto_client
      connection = @proto_client
      return connection if connection && @connected_pid == Process.pid

      @mutex.synchronize { connection_for_process }
    end

    # Startup readiness check: opens the connection in the calling process
    # (a client otherwise connects on first use) and waits until its channel
    # is ready, so a wrong or unreachable endpoint fails at startup rather
    # than on the first request. It checks the transport, not the token: a
    # rejected token surfaces as UnauthenticatedError on the first call.
    #
    # Under a forking server call it in each worker (Puma's +on_worker_boot+),
    # never in the master: connecting there counts as the parent using gRPC
    # (see "Concurrency and forking" in README.md).
    #
    # @param timeout [Numeric] seconds to wait for the channel
    # @return [self]
    # @raise [UnavailableError] the endpoint could not be reached (the channel
    #   failed to connect) within +timeout+
    # @raise [DeadlineExceededError] the channel was still connecting when
    #   +timeout+ passed
    # @raise [ForkedClientError] see #proto_client
    def connect!(timeout: DEFAULT_CONNECT_TIMEOUT_SECONDS)
      raise InvalidArgumentError, "timeout: expected a positive number of seconds, got #{Values.received(timeout)}" unless
        timeout.is_a?(Numeric) && timeout.positive?

      channel = proto_client.channel
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + timeout
      failed = false
      state = channel.connectivity_state(true)
      until state == GRPC::Core::ConnectivityStates::READY
        failed ||= state == GRPC::Core::ConnectivityStates::TRANSIENT_FAILURE
        raise UnavailableError, "spicedb: the connection to #{channel.target} is closed" if state == GRPC::Core::ConnectivityStates::FATAL_FAILURE

        remaining = deadline - Process.clock_gettime(Process::CLOCK_MONOTONIC)
        raise not_ready(channel.target, timeout, failed) unless remaining.positive?

        channel.watch_connectivity_state(state, Time.now + remaining)
        state = channel.connectivity_state(true)
      end
      self
    end

    def check(check, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:check, retry_policy)
      budget = unary_budget(timeout, call_options, retry_policy: retry_policy)
      item = check_request(check, context)
      request = Authzed::Api::V1::CheckPermissionRequest.new(**item,
        consistency: build_consistency(consistency), with_tracing: Tracing.validate(tracing) == Tracing::ENABLED)
      response = rpc(proto_client.permissions, :check_permission, request, budget)
      check_result(response, ZedToken.from_proto(response.checked_at))
    end

    # With +item_errors: BulkItemErrors::RETURNED+ each failed item's error is
    # on its CheckResult#error (never a grant) instead of failing the call.
    def check_bulk(checks, consistency:, context: nil, timeout: nil, tracing: Tracing::DISABLED,
                   item_errors: BulkItemErrors::RAISED, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:check_bulk, retry_policy)
      tracing = Tracing.validate(tracing)
      return_errors = BulkItemErrors.validate(item_errors) == BulkItemErrors::RETURNED
      budget = unary_budget(timeout, call_options, retry_policy: retry_policy)
      items = checks.to_a.map { |check| Authzed::Api::V1::CheckBulkPermissionsRequestItem.new(**check_request(check, context)) }
      cs = build_consistency(consistency)
      items.each_slice(DEFAULT_CHECK_BATCH_SIZE).with_index.flat_map do |chunk, chunk_index|
        request = Authzed::Api::V1::CheckBulkPermissionsRequest.new(consistency: cs, items: chunk, with_tracing: tracing == Tracing::ENABLED)
        response = rpc(proto_client.permissions, :check_bulk_permissions, request, budget)
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

    # An empty transaction is sent as is: SpiceDB evaluates its preconditions
    # and returns the current revision.
    #
    # Writes are never retried by default. A transaction of only touches and
    # no preconditions is idempotent on replay, so one call may opt in with
    # +retry_policy:+; any other transaction given a policy with retries
    # raises InvalidArgumentError before any request. A client-wide
    # +operation_retry_policies:+ entry for +:write+ retries only idempotent
    # transactions (Transaction#idempotent?) and leaves others unretried.
    def write(transaction, timeout: nil, metadata: nil, call_options: nil, retry_policy: nil)
      raise InvalidArgumentError, 'write: expected Transaction' unless transaction.is_a?(Transaction)

      policy = mutation_retry_policy(:write, retry_policy, transaction.idempotent?)
      budget = unary_budget(timeout, call_options, retry_policy: policy)
      updates = transaction.updates.map do |item|
        Authzed::Api::V1::RelationshipUpdate.new(operation: OPERATION_MAP.fetch(item.operation),
                                                 relationship: relationship_to_proto(item.relationship))
      end
      request = Authzed::Api::V1::WriteRelationshipsRequest.new(updates: updates,
                                                                optional_preconditions: build_preconditions(transaction.preconditions),
                                                                optional_transaction_metadata: check_context_to_struct(Values.context(metadata)))
      response = rpc(proto_client.permissions, :write_relationships, request, budget, retryable: !policy.nil?)
      WriteResult.new(revision: ZedToken.from_proto(response.written_at))
    end

    # One bounded request. With the default +partial: DeletePartial::ALLOWED+,
    # +limit+ must be a positive Integer. With +DeletePartial::DISALLOWED+ the
    # delete is all-or-nothing (the server rejects one exceeding its limit) and
    # +limit+ may be nil to use the server's default.
    def delete_relationships(filter, limit: nil, partial: DeletePartial::ALLOWED, must_match: [], must_not_match: [],
                             timeout: nil, cursor: nil, call_options: nil, retry_policy: nil)
      mutation_retry_policy(:delete_relationships, retry_policy, false)
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
                                            on_progress: nil, call_options: nil, retry_policy: nil)
      mutation_retry_policy(:delete_all_relationships_non_atomic, retry_policy, false)
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
    # filter, consistency and page size. +paging: Paging::SINGLE_REQUEST+ sends
    # one request like the raw API.
    def read_relationships(filter, consistency:, page_size: 1000, limit: nil, timeout: nil, cursor: nil, call_options: nil,
                           retry_policy: nil, paging: Paging::AUTOMATIC, &)
      retry_policy = operation_retry_policy(:read_relationships, retry_policy)
      request = Authzed::Api::V1::ReadRelationshipsRequest.new(consistency: build_consistency(consistency), relationship_filter: filter_to_proto(filter))
      mapper = lambda do |response, after|
        ReadRelationship.new(relationship: relationship_from_proto(response.relationship), cursor: after,
                             read_at: ZedToken.from_proto(response.read_at))
      end
      paged_stream(:permissions, :read_relationships, request, page_size, limit, stream_budget(timeout, call_options, retry_policy: retry_policy),
                   cursor, mapper, paging: paging, &)
    end

    def lookup_resources(resource_type:, permission:, subject:, consistency:, context: nil, limit: nil, # rubocop:disable Metrics/ParameterLists
                         page_size: 1000, timeout: nil, permissionship_filter: LookupPermissionshipFilter::ALL, cursor: nil,
                         call_options: nil, retry_policy: nil, paging: Paging::AUTOMATIC, &)
      retry_policy = operation_retry_policy(:lookup_resources, retry_policy)
      LookupPermissionshipFilter.validate(permissionship_filter)
      request = Authzed::Api::V1::LookupResourcesRequest.new(consistency: build_consistency(consistency),
                                                             resource_object_type: Values.name(resource_type, 'resource_type'),
                                                             permission: Values.name(permission, 'permission'), subject: subject_to_proto(subject),
                                                             context: check_context_to_struct(Values.context(context)))
      include_result = (->(result) { result.permissionship == :has_permission } if permissionship_filter == LookupPermissionshipFilter::HAS_PERMISSION_ONLY)
      paged_stream(:permissions, :lookup_resources, request, page_size, limit, stream_budget(timeout, call_options, retry_policy: retry_policy),
                   cursor, method(:lookup_resource_from_proto), include_result: include_result, paging: paging, &)
    end

    def lookup_subjects(resource:, permission:, subject_type:, consistency:, subject_relation: nil,
                        context: nil, timeout: nil, limit: nil, call_options: nil, retry_policy: nil, &)
      retry_policy = operation_retry_policy(:lookup_subjects, retry_policy)
      request = Authzed::Api::V1::LookupSubjectsRequest.new(consistency: build_consistency(consistency),
                                                            resource: object_to_proto(resource), permission: Values.name(permission, 'permission'),
                                                            subject_object_type: Values.name(subject_type, 'subject_type'),
                                                            optional_subject_relation: Values.optional_name(subject_relation, 'subject_relation') || '',
                                                            context: check_context_to_struct(Values.context(context)))
      budget = stream_budget(timeout, call_options, retry_policy: retry_policy)
      response_stream(:permissions, :lookup_subjects, request, budget, limit: limit, mapper: method(:lookup_subject_from_proto), &)
    end

    # Filters are ORed; empty filters watch all relationship changes.
    # Nonempty object_types and filters are mutually exclusive. Include
    # WatchContent::SCHEMA to receive schema-change events
    # (WatchEvent#schema_updated); it combines with CHECKPOINTS. Watch never
    # re-establishes its stream unless this call passes +retry_policy:+ (or
    # the client's +operation_retry_policies:+ sets one for +:watch+), which
    # then governs establishment before the first event.
    def watch(object_types: [], start_revision: nil, content: [WatchContent::RELATIONSHIPS], filters: [], timeout: nil,
              call_options: nil, retry_policy: nil, &)
      retry_policy = operation_retry_policy(:watch, retry_policy)
      response_stream(:watch, :watch, watch_request(object_types, start_revision, content,
                                                    filters: filters), stream_budget(timeout, call_options, retry_policy: retry_policy),
                      retryable: false, mapper: method(:watch_event_from_proto), &)
    end

    def read_schema(timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:read_schema, retry_policy)
      schema, revision = with_budget(timeout, call_options, operation: :read_schema, retry_policy: retry_policy) { |budget| call_read_schema(budget) }
      SchemaResult.new(schema: schema, revision: revision)
    end

    # Never retried by default; a schema write is idempotent on replay, so one
    # call may opt in with +retry_policy:+.
    def write_schema(schema, timeout: nil, call_options: nil, retry_policy: nil)
      policy = mutation_retry_policy(:write_schema, retry_policy, true)
      with_budget(timeout, call_options, retryable: !policy.nil?, operation: :write_schema, retry_policy: policy) do |budget|
        call_write_schema(schema, budget)
      end
    end

    def reflect_schema(consistency:, filters: [], timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:reflect_schema, retry_policy)
      with_budget(timeout, call_options, operation: :reflect_schema, retry_policy: retry_policy) { |budget| call_reflect_schema(consistency, budget, filters) }
    end

    def computable_permissions(definition_name:, relation_name:, consistency:, definition_name_filter: nil, timeout: nil, call_options: nil,
                               retry_policy: nil)
      retry_policy = operation_retry_policy(:computable_permissions, retry_policy)
      refs, revision = with_budget(timeout, call_options, operation: :computable_permissions, retry_policy: retry_policy) do |budget|
        call_computable_permissions(consistency, definition_name, relation_name, budget, definition_name_filter)
      end
      RelationsResult.new(relations: refs, revision: revision)
    end

    def dependent_relations(definition_name:, permission_name:, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:dependent_relations, retry_policy)
      refs, revision = with_budget(timeout, call_options, operation: :dependent_relations, retry_policy: retry_policy) do |budget|
        call_dependent_relations(consistency, definition_name, permission_name, budget)
      end
      RelationsResult.new(relations: refs, revision: revision)
    end

    def diff_schema(comparison_schema, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:diff_schema, retry_policy)
      diffs, revision = with_budget(timeout, call_options, operation: :diff_schema, retry_policy: retry_policy) do |budget|
        call_diff_schema(consistency, comparison_schema, budget)
      end
      SchemaDiffResult.new(diffs: diffs, revision: revision)
    end

    def expand_permission_tree(resource:, permission:, consistency:, timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:expand_permission_tree, retry_policy)
      with_budget(timeout, call_options, operation: :expand_permission_tree, retry_policy: retry_policy) do |budget|
        call_expand_permission_tree(consistency, resource.object_type, resource.object_id, permission, budget)
      end
    end

    # One call is one stream and one server transaction; call again for
    # several transactions. +batch_size+ is relationships per message. When
    # the server aborts the stream, the server's status is raised (for
    # example AlreadyExistsError for a duplicate). Never retried; a
    # +retry_policy:+ with retries raises InvalidArgumentError.
    def import_relationships(relationships, timeout: nil, batch_size: DEFAULT_IMPORT_BATCH_SIZE, call_options: nil, retry_policy: nil)
      mutation_retry_policy(:import_relationships, retry_policy, false)
      positive(batch_size, 'batch_size')
      budget = stream_budget(timeout, call_options)
      with_retry(budget: budget, retryable: false) { call_import_relationships(relationships, budget, batch_size) }
    end

    # Yields ExportedRelationship items carrying their response's cursor.
    # Resume by passing it as +cursor:+ with the same filter, consistency and
    # page size.
    def export_relationships(filter = nil, consistency:, page_size: 1000, timeout: nil, limit: nil, cursor: nil, call_options: nil,
                             retry_policy: nil, &)
      retry_policy = operation_retry_policy(:export_relationships, retry_policy)
      request = Authzed::Api::V1::ExportBulkRelationshipsRequest.new(consistency: build_consistency(consistency),
                                                                     optional_relationship_filter: filter && filter_to_proto(filter),
                                                                     optional_cursor: cursor_proto(cursor))
      # Export responses contain batches, unlike the per-item read API.
      export_stream(request, page_size, limit, stream_budget(timeout, call_options, retry_policy: retry_policy), &)
    end

    def experimental_register_relationship_counter(name, filter, timeout: nil, call_options: nil, retry_policy: nil)
      mutation_retry_policy(:experimental_register_relationship_counter, retry_policy, false)
      with_budget(timeout, call_options, retryable: false) { |budget| call_register_relationship_counter(name, filter, budget) }
    end

    def experimental_count_relationships(name, timeout: nil, call_options: nil, retry_policy: nil)
      retry_policy = operation_retry_policy(:experimental_count_relationships, retry_policy)
      with_budget(timeout, call_options, operation: :experimental_count_relationships, retry_policy: retry_policy) do |budget|
        call_count_relationships(name, budget)
      end
    end

    def experimental_unregister_relationship_counter(name, timeout: nil, call_options: nil, retry_policy: nil)
      mutation_retry_policy(:experimental_unregister_relationship_counter, retry_policy, false)
      with_budget(timeout, call_options, retryable: false) { |budget| call_unregister_relationship_counter(name, budget) }
    end

    def experimental_roaring_lookup_resources(resource_type:, permission:, subject:, consistency:, timeout: nil, call_options: nil,
                                              retry_policy: nil)
      retry_policy = operation_retry_policy(:experimental_roaring_lookup_resources, retry_policy)
      budget = unary_budget(timeout, call_options, retry_policy: retry_policy)
      request = Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesRequest.new(
        consistency: build_consistency(consistency), resource_object_type: Values.name(resource_type, 'resource_type'),
        permission: Values.name(permission, 'permission'), subject: subject_to_proto(subject)
      )
      response = rpc(proto_client.materialize, :experimental_roaring_lookup_resources, request, budget)
      RoaringLookupResourcesResult.new(bitmap: response.bitmap, cardinality: response.cardinality,
                                       at_revision: ZedToken.from_proto(response.at_revision))
    end

    private

    # Called under @mutex. Opens the connection in this process, or explains
    # why a connection opened before a fork cannot be used here.
    def connection_for_process
      pid = Process.pid
      # @connected_pid is nil only for a protocol client installed directly
      # (test fakes), which is used as is.
      return @proto_client if @proto_client && (@connected_pid.nil? || @connected_pid == pid)

      if @proto_client
        raise ForkedClientError.new(connected_pid: @connected_pid, current_pid: pid) unless SpiceDB.grpc_fork_support?

        # grpc's fork support carries the parent's channel into this process.
        return @proto_client
      end
      raise Error, 'client is closed' if @closed

      # A new client in a child: gRPC refuses it if an ancestor process
      # already used gRPC, whichever client did.
      used = SpiceDB.grpc_used_pid
      if used && used != pid && !SpiceDB.grpc_fork_support?
        raise ForkedClientError.new(ForkedClientError.fresh_client_message(used, pid), connected_pid: used, current_pid: pid)
      end

      adopt_connection(@connect.call)
    rescue ArgumentError => e
      raise InvalidArgumentError, e.message
    rescue RuntimeError => e
      # gRPC used before the fork in a way this library could not observe
      # (another gRPC client in the parent): grpc's own refusal, typed.
      raise unless ForkedClientError.grpc_fork_refusal?(e)

      raise ForkedClientError.new(ForkedClientError.fresh_client_message(nil, pid), current_pid: pid)
    end

    def not_ready(target, timeout, failed)
      return UnavailableError.new("spicedb: could not connect to #{target} within #{timeout}s (unreachable or refusing connections)") if failed

      DeadlineExceededError.new("spicedb: the connection to #{target} was not ready within #{timeout}s")
    end

    def adopt_connection(connection)
      SpiceDB.note_grpc_use
      @connected_pid = Process.pid
      @proto_client = connection
    end

    def configure(default_timeout: DEFAULT_TIMEOUT_SECONDS, default_deadline: DefaultDeadline::ENABLED, retry_policy: RetryPolicy.new,
                  retries: Retries::ENABLED, stream_retries: StreamRetries::ENABLED, operation_retry_policies: {})
      Budget.new(default_timeout)
      raise InvalidArgumentError, 'default_timeout is required' if default_timeout.nil?
      raise InvalidArgumentError, 'retry_policy: expected SpiceDB::RetryPolicy' unless retry_policy.is_a?(RetryPolicy)

      @default_timeout = default_timeout
      @default_deadline = DefaultDeadline.validate(default_deadline)
      @retry_policy = retry_policy
      @retries = Retries.validate(retries)
      @stream_retries = StreamRetries.validate(stream_retries)
      @operation_retry_policies = Retrying.operation_policies(operation_retry_policies)
      @streams = []
      @mutex = Mutex.new
      @closed = false
    end
  end
end
