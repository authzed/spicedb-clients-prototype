# frozen_string_literal: true

module SpiceDB
  module Retrying
    MAX_RETRIES = 3
    BASE_RETRY_DELAY = 0.1

    private

    def deadline_for(budget)
      budget.is_a?(Budget) ? budget.deadline : Budget.new(budget).deadline
    end

    def backoff_delay(attempt) = rand * (BASE_RETRY_DELAY * (2**(attempt - 1)))

    def with_retry(budget:, max_retries: @max_retries)
      require_proto_client!
      attempt = 0
      begin
        budget.remaining
        yield
      rescue StandardError => e
        if SpiceDB.transient?(e) && attempt < max_retries
          attempt += 1
          budget.sleep(backoff_delay(attempt))
          retry
        end
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end
  end
end
