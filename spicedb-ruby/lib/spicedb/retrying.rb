# frozen_string_literal: true

module SpiceDB
  # Retry helpers shared by every RPC-issuing method on {SpiceDB::Client}.
  # Extracted the same way {SpiceDB::CaveatContext} is, to keep Client's own
  # body focused on request building and response mapping.
  #
  # Per root DESIGN.md's "RULE: Automatic retry is for idempotent operations
  # only": reads go through {#with_retry} and retry on a transient gRPC
  # error; mutations go through {#call_once} and are never retried, since a
  # `WriteRelationships` carrying `OPERATION_CREATE` or preconditions is not
  # idempotent -- if it commits and the response is lost (a rolling
  # restart, a proxy dropping the connection), a retry would surface
  # `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in fact
  # succeeded, and the caller would wrongly conclude it had failed.
  module Retrying
    # Retry configuration for transient gRPC errors.
    MAX_RETRIES = 3
    BASE_RETRY_DELAY = 0.1 # seconds

    private

    # Resolves a per-call `timeout:` (seconds, or nil) against the client's
    # `@default_timeout`. `nil` (the default on every unary method) means
    # "use @default_timeout" -- there is deliberately no way to request an
    # unbounded unary call. See root DESIGN.md, "RULE: A unary call must have
    # a deadline".
    def effective_timeout(timeout)
      timeout || @default_timeout
    end

    # Converts a relative timeout (seconds) into the absolute `Time` grpc-ruby's
    # generated stubs expect for their `deadline:` call option. Computed fresh
    # at the point of each individual RPC attempt (not once per public method
    # call) so a retried call gets a full new window per attempt, matching
    # grpc-python's per-attempt `timeout=` semantics.
    #
    # `nil` in, `nil` out (no deadline at all) -- used by client-streaming
    # calls like `import_relationships`, which deliberately skip
    # `effective_timeout` and so may pass `nil` straight through here.
    def deadline_for(timeout_seconds)
      return nil if timeout_seconds.nil?

      Time.now + timeout_seconds
    end

    # Full-jitter backoff delay in seconds for retry attempt `attempt`
    # (1-indexed, matching `with_retry`'s `attempts`): `uniform(0, cap)`
    # rather than the fixed `cap`. Without jitter, every client in a fleet
    # retries on the same schedule after a server restart, turning the
    # recovery into a thundering herd; sampling uniformly under the cap
    # spreads retries out instead.
    def backoff_delay(attempt)
      rand * (BASE_RETRY_DELAY * (2**(attempt - 1)))
    end

    # Retries the block with full-jitter exponential backoff for transient
    # gRPC errors. Only for idempotent (read) calls -- see {#call_once} for
    # mutations.
    def with_retry(max_retries: MAX_RETRIES)
      require_proto_client!
      attempts = 0
      begin
        yield
      rescue StandardError => e
        attempts += 1
        if SpiceDB.transient?(e) && attempts <= max_retries
          sleep(backoff_delay(attempts))
          retry
        end
        raise SpiceDB.to_spicedb_error(e) if e.respond_to?(:code)

        raise
      end
    end

    # Runs the block once, converting a gRPC error, but never retrying --
    # {#with_retry} with a zero retry budget converts an error on the first
    # failure exactly as `call_once` needs. For mutations; see the module
    # doc above for why they must not auto-retry.
    def call_once(&)
      with_retry(max_retries: 0, &)
    end
  end
end
