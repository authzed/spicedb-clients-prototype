# frozen_string_literal: true

module SpiceDB
  module Retrying
    MAX_RETRIES = 3
    BASE_RETRY_DELAY = 0.1

    # @api private
    #
    # The per-call RetryPolicy a mutation may use, or nil. Mutations are never
    # retried by default; a caller may opt one call in only where replay is
    # idempotent (+idempotent+: a touch-only write without preconditions, or a
    # schema write). Every other mutation given a policy with retries raises
    # InvalidArgumentError, before any request is sent.
    def self.mutation_policy(policy, idempotent, operation)
      raise InvalidArgumentError, 'retry_policy: expected SpiceDB::RetryPolicy' unless policy.nil? || policy.is_a?(RetryPolicy)
      return nil if policy.nil? || policy.max_retries.zero?
      unless idempotent
        raise InvalidArgumentError, "#{operation} cannot be retried: only touch-only writes without preconditions and schema writes are " \
                                    'idempotent on replay'
      end

      policy
    end

    # @api private
    #
    # Whether replaying +transaction+ is idempotent: only touches, and no
    # preconditions.
    def self.idempotent_write?(transaction)
      transaction.preconditions.empty? && transaction.updates.all? { |update| update.operation == :touch }
    end

    private

    def deadline_for(budget)
      budget.is_a?(Budget) ? budget.deadline : Budget.new(budget).deadline
    end

    def backoff_delay(attempt) = @retry_policy.delay(attempt)

    def notify_retry(policy, operation, attempt, error, delay)
      policy.on_retry&.call(RetryEvent.new(operation: operation&.to_s, attempt: attempt, error: SpiceDB.to_spicedb_error(error), delay: delay))
    end

    # The policy one operation runs under: the call's own +retry_policy:+
    # wins; otherwise the client's, unless retries are disabled.
    def call_retry_policy(budget)
      return budget.retry_policy if budget.is_a?(Budget) && budget.retry_policy

      @retries == Retries::ENABLED ? @retry_policy : nil
    end

    # Retries only when +retryable+ (mutations pass true only after a
    # per-call opt-in) and the effective RetryPolicy names the error's code.
    def with_retry(budget:, retryable: true, operation: nil)
      require_proto_client!
      policy = retryable ? call_retry_policy(budget) : nil
      attempt = 0
      begin
        budget.remaining
        yield
      rescue StandardError => e
        if policy && attempt < policy.max_retries && policy.retryable?(e)
          attempt += 1
          delay = policy.delay(attempt)
          notify_retry(policy, operation, attempt, e, delay)
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
