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
    # Validates the client's +operation_retry_policies:+ (operation name =>
    # RetryPolicy) and returns a frozen copy. Operations that are never
    # idempotent on replay accept only a policy without retries.
    def self.operation_policies(policies)
      raise InvalidArgumentError, 'operation_retry_policies: expected a Hash of operation => RetryPolicy' unless policies.is_a?(Hash)

      policies.to_h do |operation, policy|
        unless Client::OPERATIONS.include?(operation)
          raise InvalidArgumentError, "operation_retry_policies: unknown operation #{operation.inspect}; expected one of Client::OPERATIONS"
        end
        raise InvalidArgumentError, "operation_retry_policies: #{operation}: expected SpiceDB::RetryPolicy" unless policy.is_a?(RetryPolicy)
        if policy.max_retries.positive? && Client::NEVER_IDEMPOTENT_OPERATIONS.include?(operation)
          raise InvalidArgumentError, "#{operation} cannot be retried: it is not idempotent on replay"
        end

        [operation, policy]
      end.freeze
    end

    private

    # The policy a read or stream runs under before the client-wide one: the
    # call's own +retry_policy:+, else the client's policy for +operation+.
    def operation_retry_policy(operation, per_call) = per_call.nil? ? @operation_retry_policies[operation] : per_call

    # The policy a mutation runs under, or nil. A per-call +retry_policy:+
    # wins (and raises for a non-idempotent call, see ::mutation_policy); the
    # client's policy for +operation+ applies only when this call is
    # +idempotent+, and leaves other calls unretried without an error.
    def mutation_retry_policy(operation, per_call, idempotent)
      policy = Retrying.mutation_policy(per_call, idempotent, operation.to_s)
      return policy unless per_call.nil?

      configured = @operation_retry_policies[operation]
      configured if idempotent && configured&.max_retries&.positive?
    end

    def deadline_for(budget)
      budget.is_a?(Budget) ? budget.deadline : Budget.new(budget).deadline
    end

    def backoff_delay(attempt) = @retry_policy.delay(attempt)

    def notify_retry(policy, operation, attempt, error, delay)
      policy.on_retry&.call(RetryEvent.new(operation: operation&.to_s, attempt: attempt, error: SpiceDB.to_spicedb_error(error), delay: delay))
    end

    # The policy one operation runs under: the call's own +retry_policy:+ (or
    # the client's policy for that operation, already resolved into the
    # budget) wins; otherwise the client's, unless retries are disabled.
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
