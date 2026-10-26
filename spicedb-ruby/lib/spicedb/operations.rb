# frozen_string_literal: true

module SpiceDB
  module Operations
    private

    def unary_budget(timeout)
      raise Error, 'client is closed' if @closed

      Budget.new(timeout || @default_timeout)
    end

    def with_budget(timeout, retryable: true)
      budget = unary_budget(timeout)
      with_retry(budget: budget, max_retries: retryable ? @max_retries : 0) { yield budget }
    end

    def rpc(service, operation, request, budget, retryable: true)
      with_retry(budget: budget, max_retries: retryable ? @max_retries : 0) do
        service.public_send(operation, request, deadline: budget.deadline)
      end
    end

    def positive(value, name)
      raise InvalidArgumentError, "#{name} must be a positive integer" unless value.is_a?(Integer) && value.positive?

      value
    end

    def object_to_proto(object)
      raise InvalidArgumentError, 'expected ObjectRef' unless object.is_a?(ObjectRef)

      Authzed::Api::V1::ObjectReference.new(object_type: object.object_type, object_id: object.object_id)
    end

    def subject_to_proto(subject)
      raise InvalidArgumentError, 'expected SubjectRef' unless subject.is_a?(SubjectRef)

      Authzed::Api::V1::SubjectReference.new(object: object_to_proto(subject.object), optional_relation: subject.relation || '')
    end

    def check_request(check, context)
      raise InvalidArgumentError, 'check: expected PermissionCheck' unless check.is_a?(PermissionCheck)

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

    def delete_page(filter, limit, must_match, must_not_match, cursor, budget)
      preconditions = must_match.map { |item| { operation: :must_match, filter: item } } +
                      must_not_match.map { |item| { operation: :must_not_match, filter: item } }
      request = Authzed::Api::V1::DeleteRelationshipsRequest.new(relationship_filter: filter_to_proto(filter),
                                                                 optional_preconditions: build_preconditions(preconditions), optional_limit: limit,
                                                                 optional_allow_partial_deletions: true,
                                                                 optional_cursor: cursor && !cursor.empty? ? Authzed::Api::V1::Cursor.new(token: cursor) : nil)
      response = rpc(@proto_client.permissions, :delete_relationships, request, budget, retryable: false)
      progress = { DELETION_PROGRESS_COMPLETE: :complete, DELETION_PROGRESS_PARTIAL: :partial }.fetch(response.deletion_progress, :unspecified)
      result = DeleteResult.new(revision: ZedToken.from_proto(response.deleted_at), deleted_count: response.relationships_deleted_count,
                                progress: progress, cursor: response.after_result_cursor&.token)
      if progress == :unspecified
        error = Error.new('delete returned unknown progress')
        error.instance_variable_set(:@delete_progress, result)
        raise error
      end
      result
    end

    def make_stream(&)
      stream = Stream.new(on_close: ->(closed) { @mutex.synchronize { @streams.delete(closed) } }, &)
      @mutex.synchronize do
        raise Error, 'client is closed' if @closed

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

    def consume_rpc(stream, service, operation, request, budget, retryable: true)
      produced = 0
      attempt = 0
      begin
        call = @proto_client.public_send(service).public_send(operation, request, deadline: budget.deadline, return_op: true)
        stream.attach(call)
        call.execute.each do |response|
          budget.remaining
          produced += 1
          yield response
        end
      rescue StandardError => e
        raise StopIteration if stream.closed?

        if retryable && produced.zero? && SpiceDB.transient?(e) && attempt < @max_retries
          call&.cancel
          stream.detach(call) if call
          call = nil
          attempt += 1
          budget.sleep(backoff_delay(attempt))
          retry
        end
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      ensure
        call&.cancel
        stream.detach(call) if call
      end
    end

    def response_stream(service, operation, request, timeout, mapper:, retryable: true, limit: nil, &)
      budget = Budget.new(timeout)
      positive(limit, 'limit') if limit
      stream = make_stream do |owner, out|
        delivered = 0
        consume_rpc(owner, service, operation, request, budget, retryable: retryable) do |response|
          out << mapper.call(response)
          delivered += 1
          break if limit && delivered >= limit
        end
      end
      return_stream(stream, &)
    end

    def paged_stream(service, operation, request, page_size, limit, timeout, limit_field, cursor_field, response_cursor, mapper, include_result: nil, &) # rubocop:disable Metrics/ParameterLists
      positive(page_size, 'page_size')
      positive(limit, 'limit') if limit
      budget = Budget.new(timeout)
      stream = make_stream do |owner, out|
        delivered = 0
        cursor = nil
        loop do
          count = 0
          page_limit = limit ? [page_size, limit - delivered].min : page_size
          request.public_send("#{limit_field}=", page_limit)
          request.public_send("#{cursor_field}=", cursor && Authzed::Api::V1::Cursor.new(token: cursor))
          previous = cursor
          consume_rpc(owner, service, operation, request, budget) do |response|
            cursor = response.public_send(response_cursor)&.token
            # Pin later pages to the first response's revision.
            token = response.respond_to?(:read_at) ? response.read_at : response.looked_up_at
            request.consistency = Authzed::Api::V1::Consistency.new(at_exact_snapshot: token) if token && !token.token.empty?
            count += 1
            result = mapper.call(response)
            next if include_result && !include_result.call(result)

            delivered += 1
            out << result
          end
          break if count < page_limit || (limit && delivered >= limit)
          raise Error, 'paged stream did not advance its cursor' if cursor.nil? || cursor.empty? || cursor == previous
        end
      end
      return_stream(stream, &)
    end

    def export_stream(request, page_size, limit, timeout, &)
      positive(page_size, 'page_size')
      positive(limit, 'limit') if limit
      request.optional_limit = page_size
      budget = Budget.new(timeout)
      stream = make_stream do |owner, out|
        delivered = 0
        consume_rpc(owner, :permissions, :export_bulk_relationships, request, budget) do |response|
          response.relationships.each do |relationship|
            break if limit && delivered >= limit

            delivered += 1
            out << relationship_from_proto(relationship)
          end
          break if limit && delivered >= limit
        end
      end
      return_stream(stream, &)
    end
  end
end
