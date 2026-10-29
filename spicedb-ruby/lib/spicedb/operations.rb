# frozen_string_literal: true

module SpiceDB
  module Operations
    MAX_UINT32 = (2**32) - 1
    private_constant :MAX_UINT32

    private

    def unary_budget(timeout, call_options = nil, retry_policy: nil)
      raise FailedPreconditionError, 'client is closed' if @closed

      Budget.new(timeout || (@default_deadline == DefaultDeadline::ENABLED ? @default_timeout : nil), call_options: call_options,
                                                                                                      retry_policy: retry_policy)
    end

    def stream_budget(timeout, call_options = nil, retry_policy: nil)
      Budget.new(timeout, call_options: call_options, retry_policy: retry_policy)
    end

    # A finite paged read (relationship reads, lookups, exports): +timeout+
    # bounds the whole operation, and the client default deadline bounds each
    # page request (Budget#page). Watch and imports use #stream_budget.
    def paged_budget(timeout, call_options = nil, retry_policy: nil)
      page_timeout = @default_deadline == DefaultDeadline::ENABLED ? @default_timeout : nil
      Budget.new(timeout, call_options: call_options, retry_policy: retry_policy, page_timeout: page_timeout)
    end

    def with_budget(timeout, call_options = nil, retryable: true, operation: nil, retry_policy: nil)
      budget = unary_budget(timeout, call_options, retry_policy: retry_policy)
      with_retry(budget: budget, retryable: retryable, operation: operation) { yield budget }
    end

    def rpc(service, operation, request, budget, retryable: true)
      with_retry(budget: budget, retryable: retryable, operation: operation) do
        invoke(service, operation, request, budget)
      end
    end

    def positive(value, name)
      raise LocalValidationError, "#{name} must be a positive integer" unless value.is_a?(Integer) && value.positive?

      value
    end

    def object_to_proto(object)
      raise LocalValidationError, 'expected ObjectRef' unless object.is_a?(ObjectRef)

      Authzed::Api::V1::ObjectReference.new(object_type: object.object_type, object_id: object.object_id)
    end

    def subject_to_proto(subject)
      raise LocalValidationError, 'expected SubjectRef' unless subject.is_a?(SubjectRef)

      Authzed::Api::V1::SubjectReference.new(object: object_to_proto(subject.object), optional_relation: subject.relation || '')
    end

    def check_request(check, context)
      raise LocalValidationError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

      { resource: object_to_proto(check.resource), permission: check.permission, subject: subject_to_proto(check.subject),
        context: check_context_to_struct(merge_check_context(Values.context(context), check.context)) }
    end

    def check_result(response, token)
      CheckResult.new(permissionship: check_permissionship_from_proto(response.permissionship),
                      missing_context: missing_context_from_proto(response.partial_caveat_info), checked_at: token,
                      debug_trace: debug_information(response.debug_trace),
                      expiration: if response.respond_to?(:optional_expires_at) && response.optional_expires_at
                                    Time.at(response.optional_expires_at.seconds, response.optional_expires_at.nanos, :nsec)
                                  end)
    end

    def delete_page(filter, limit, must_match, must_not_match, cursor, budget, partial: DeletePartial::ALLOWED)
      preconditions = must_match.map { |item| Precondition.new(operation: :must_match, filter: item) } +
                      must_not_match.map { |item| Precondition.new(operation: :must_not_match, filter: item) }
      request = Authzed::Api::V1::DeleteRelationshipsRequest.new(relationship_filter: filter_to_proto(filter),
                                                                 optional_preconditions: build_preconditions(preconditions), optional_limit: limit || 0,
                                                                 optional_allow_partial_deletions: partial == DeletePartial::ALLOWED,
                                                                 optional_cursor: cursor_proto(cursor))
      response = rpc(proto_client.permissions, :delete_relationships, request, budget, retryable: false)
      progress = { DELETION_PROGRESS_COMPLETE: :complete, DELETION_PROGRESS_PARTIAL: :partial }.fetch(response.deletion_progress, :unspecified)
      result = DeleteResult.new(revision: ZedToken.from_proto(response.deleted_at), deleted_count: response.relationships_deleted_count,
                                progress: progress, cursor: response.after_result_cursor&.token)
      if progress == :unspecified
        error = malformed_response('delete_relationships returned unknown progress')
        error.instance_variable_set(:@delete_progress, result)
        raise error
      end
      result
    end

    def make_stream(&)
      stream = Stream.new(on_close: ->(closed) { @mutex.synchronize { @streams.delete(closed) } }, &)
      @mutex.synchronize do
        raise FailedPreconditionError, 'client is closed' if @closed

        @streams << stream
      end
      stream
    end

    def return_stream(stream, &)
      return stream unless block_given?

      stream.each(&)
    ensure
      stream.close if block_given?
    end

    # The policy that may re-establish a stream before it exposes an item: the
    # call's own +retry_policy:+ (which also overrides StreamRetries::DISABLED
    # and watch's default of never re-establishing), else the client's.
    def stream_retry_policy(budget, retryable)
      return budget.retry_policy if budget.retry_policy
      return nil unless retryable && @stream_retries == StreamRetries::ENABLED

      call_retry_policy(budget)
    end

    # +page_timeout:+ bounds each response of a call that streams several
    # pages (see PageWatchdog).
    def consume_rpc(stream, service, operation, request, budget, retryable: true, page_timeout: nil)
      policy = stream_retry_policy(budget, retryable)
      produced = 0
      attempt = 0
      begin
        call = proto_client.public_send(service).public_send(operation, request, **stub_kwargs(budget), return_op: true)
        stream.attach(call)
        watchdog = page_timeout && PageWatchdog.new(page_timeout, call)
        call.execute.each do |response|
          watchdog&.received
          budget.remaining
          produced += 1
          yield response
          watchdog&.waiting
        end
        record_metadata(budget, call)
      rescue StandardError => e
        raise StopIteration if stream.closed?

        watchdog&.stop
        raise DeadlineExceededError, "#{operation}: no page arrived within the #{page_timeout}s per-page deadline" if watchdog&.expired?

        record_metadata(budget, call, e) if call && e.is_a?(GRPC::BadStatus)
        if policy && produced.zero? && attempt < policy.max_retries && policy.retryable?(e)
          call&.cancel
          stream.detach(call) if call
          call = nil
          attempt += 1
          delay = policy.delay(attempt)
          notify_retry(policy, operation, attempt, e, delay)
          budget.sleep(delay)
          retry
        end
        raise if e.is_a?(Error)
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      ensure
        watchdog&.stop
        call&.cancel
        stream.detach(call) if call
      end
    end

    # One call streams every result. A finite read's per-page default
    # (Budget#page_timeout) bounds each wait for the next response, never the
    # time the caller spends on an item (see PageWatchdog); watch has none.
    def response_stream(service, operation, request, budget, mapper:, retryable: true, limit: nil, &)
      positive(limit, 'limit') if limit
      stream = make_stream do |owner, out|
        delivered = 0
        consume_rpc(owner, service, operation, request, budget, retryable: retryable, page_timeout: budget.page_timeout) do |response|
          out << mapper.call(response)
          delivered += 1
          break if limit && delivered >= limit
        end
      end
      return_stream(stream, &)
    end

    # Every page repeats the caller's consistency and the same page size:
    # SpiceDB hashes both into the cursor and rejects a cursor sent with
    # different arguments. The cursor itself pins later pages to the first
    # page's revision. A total limit is enforced here, never by shrinking the
    # last page's limit.
    #
    # With +paging: Paging::SINGLE_REQUEST+ exactly one request is sent, as
    # the raw API does: its limit is +limit+ (0, unbounded, without one), the
    # page size is ignored, and no follow-up page is requested.
    #
    # The per-page default deadline never counts the caller's processing
    # time. An automatic page (at most +page_size+ responses) is received
    # whole under its page deadline (Budget#page) and only then yielded. A
    # single request has no size bound, so it is not buffered: the default
    # bounds each wait for the next response instead (PageWatchdog).
    def paged_stream(service, operation, request, page_size, limit, budget, cursor, mapper, include_result: nil, paging: Paging::AUTOMATIC, &)
      positive(page_size, 'page_size')
      positive(limit, 'limit') if limit
      single = Paging.validate(paging) == Paging::SINGLE_REQUEST
      stream = make_stream do |owner, out|
        delivered = 0
        loop do
          count = 0
          request.optional_limit = single ? [limit || 0, MAX_UINT32].min : page_size
          request.optional_cursor = cursor_proto(cursor)
          previous = cursor
          deliver = lambda do |response|
            cursor = response.after_result_cursor&.token
            count += 1
            result = mapper.call(response, cursor)
            next false if include_result && !include_result.call(result)

            delivered += 1
            out << result
            limit && delivered >= limit
          end
          if single
            consume_rpc(owner, service, operation, request, budget, page_timeout: budget.page_timeout) do |response|
              break if deliver.call(response)
            end
          else
            page = []
            consume_rpc(owner, service, operation, request, budget.page) { |response| page << response }
            page.each { |response| break if deliver.call(response) }
          end
          break if single || count < page_size || (limit && delivered >= limit)
          raise malformed_response("#{operation}: the page cursor did not advance") if cursor.nil? || cursor.empty? || cursor == previous
        end
      end
      return_stream(stream, &)
    end

    def export_stream(request, page_size, limit, budget, &)
      positive(page_size, 'page_size')
      positive(limit, 'limit') if limit
      request.optional_limit = page_size
      stream = make_stream do |owner, out|
        delivered = 0
        consume_rpc(owner, :permissions, :export_bulk_relationships, request, budget, page_timeout: budget.page_timeout) do |response|
          cursor = response.after_result_cursor&.token
          response.relationships.each do |relationship|
            break if limit && delivered >= limit

            delivered += 1
            out << ExportedRelationship.new(relationship: relationship_from_proto(relationship), cursor: cursor)
          end
          break if limit && delivered >= limit
        end
      end
      return_stream(stream, &)
    end
  end
end
