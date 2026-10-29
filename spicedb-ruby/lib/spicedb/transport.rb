# frozen_string_literal: true

module SpiceDB
  # Stub invocation with per-call transport options (CallOptions) and
  # response-metadata capture, shared by every operation.
  module Transport
    private

    # Every unary stub call goes through here, so per-call transport options
    # and response-metadata capture apply to every operation, attempt, page
    # and chunk.
    def invoke(service, operation, request, budget)
      options = budget.is_a?(Budget) ? budget.call_options : nil
      kwargs = stub_kwargs(budget)
      return service.public_send(operation, request, **kwargs) unless options&.capture?

      call = service.public_send(operation, request, **kwargs, return_op: true)
      begin
        response = call.execute
        record_metadata(budget, call)
        response
      rescue GRPC::BadStatus => e
        record_metadata(budget, call, e)
        raise
      end
    end

    def stub_kwargs(budget)
      budget = Budget.new(budget) unless budget.is_a?(Budget)
      kwargs = { deadline: budget.deadline }
      kwargs.merge!(budget.call_options.stub_kwargs) if budget.call_options
      kwargs
    end

    def record_metadata(budget, call, error = nil)
      capture = budget.is_a?(Budget) ? budget.call_options&.response_metadata : nil
      return unless capture

      header = call.respond_to?(:metadata) ? call.metadata : nil
      trailer = if error
                  error.metadata
                elsif call.respond_to?(:trailing_metadata)
                  call.trailing_metadata
                end
      capture.record(header: header, trailer: trailer)
    end

    # The status a client-streaming call finished with, when a send failed
    # because the server had already ended the call.
    def final_status(call)
      status = call.respond_to?(:status) ? call.status : nil
      return nil if status.nil? || status.code == GRPC::Core::StatusCodes::OK

      GRPC::BadStatus.new_status_exception(status.code, status.details, status.metadata)
    rescue StandardError
      nil
    end

    def cursor_proto(cursor) = cursor && !cursor.empty? ? Authzed::Api::V1::Cursor.new(token: cursor) : nil
  end
end
