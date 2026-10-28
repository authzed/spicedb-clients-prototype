# frozen_string_literal: true

module SpiceDB
  module Retrying
    MAX_RETRIES = 3
    BASE_RETRY_DELAY = 0.1

    private

    def deadline_for(budget)
      budget.is_a?(Budget) ? budget.deadline : Budget.new(budget).deadline
    end

    def backoff_delay(attempt) = @retry_policy.delay(attempt)

    def notify_retry(operation, attempt, error, delay)
      callback = @retry_policy.on_retry
      callback&.call(RetryEvent.new(operation: operation&.to_s, attempt: attempt, error: SpiceDB.to_spicedb_error(error), delay: delay))
    end

    # Retries only when +retryable+ (never for mutations), retries are
    # enabled, and the client's RetryPolicy names the error's code.
    def with_retry(budget:, retryable: true, operation: nil)
      require_proto_client!
      attempt = 0
      begin
        budget.remaining
        yield
      rescue StandardError => e
        if retryable && @retries == Retries::ENABLED && attempt < @retry_policy.max_retries && @retry_policy.retryable?(e)
          attempt += 1
          delay = @retry_policy.delay(attempt)
          notify_retry(operation, attempt, e, delay)
          budget.sleep(delay)
          retry
        end
        raise if e.is_a?(Error)
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end
  end
end
