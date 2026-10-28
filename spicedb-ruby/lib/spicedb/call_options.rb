# frozen_string_literal: true

module SpiceDB
  # Client-wide read retry policy. The default retries UNAVAILABLE and ABORTED
  # up to three times with full-jitter exponential backoff from 100 ms. A
  # caller may name other read codes, including RESOURCE_EXHAUSTED; mutations
  # never retry under any policy.
  #
  # +on_retry+ is called before each backoff with a RetryEvent.
  RetryPolicy = Data.define(:max_retries, :retryable_codes, :initial_backoff, :max_backoff, :on_retry) do
    def initialize(max_retries: 3, retryable_codes: TRANSIENT_CODES, initial_backoff: 0.1, max_backoff: nil, on_retry: nil)
      raise InvalidArgumentError, 'retry policy: max_retries must be a nonnegative Integer' unless max_retries.is_a?(Integer) && !max_retries.negative?
      raise InvalidArgumentError, 'retry policy: initial_backoff must be positive seconds' unless initial_backoff.is_a?(Numeric) && initial_backoff.positive?
      unless max_backoff.nil? || (max_backoff.is_a?(Numeric) && max_backoff.positive?)
        raise InvalidArgumentError, 'retry policy: max_backoff must be positive seconds or nil'
      end
      unless retryable_codes.respond_to?(:to_a) && retryable_codes.to_a.all?(Integer)
        raise InvalidArgumentError, 'retry policy: retryable_codes must be GRPC::Core::StatusCodes values'
      end
      raise InvalidArgumentError, 'retry policy: on_retry must respond to call' unless on_retry.nil? || on_retry.respond_to?(:call)

      super(max_retries: max_retries, retryable_codes: retryable_codes.to_a.uniq.freeze, initial_backoff: initial_backoff,
            max_backoff: max_backoff, on_retry: on_retry)
    end

    def retryable?(error) = retryable_codes.include?(SpiceDB.code_of(error))

    # Full jitter: a uniform delay up to the attempt's exponential ceiling.
    def delay(attempt)
      ceiling = initial_backoff * (2**(attempt - 1))
      ceiling = [ceiling, max_backoff].min if max_backoff
      rand * ceiling
    end
  end

  # What a RetryPolicy's +on_retry+ callback receives.
  RetryEvent = Data.define(:operation, :attempt, :error, :delay)

  # Captures a call's response headers and trailers. Pass one through
  # CallOptions; after the operation (successful or not) +header+ and
  # +trailer+ hold the last attempt's metadata. Paged and chunked operations
  # record the last request's metadata.
  class ResponseMetadata
    attr_reader :header, :trailer

    def initialize
      @header = {}
      @trailer = {}
    end

    # @api private
    def record(header: nil, trailer: nil)
      @header = header.to_h.dup.freeze if header
      @trailer = trailer.to_h.dup.freeze if trailer
    end
  end

  # The per-call transport escape hatch every operation accepts as
  # +call_options:+. +metadata+ is sent as request headers on every attempt,
  # page and chunk; +credentials+ is a GRPC::Core::CallCredentials composed
  # onto the call; +response_metadata+ captures headers and trailers. This is
  # the supported route to transport-level data, including what SpiceDB
  # versions before 1.30 sent in trailers.
  CallOptions = Data.define(:metadata, :credentials, :response_metadata) do
    def initialize(metadata: {}, credentials: nil, response_metadata: nil)
      raise InvalidArgumentError, 'call_options.metadata must be a Hash' unless metadata.is_a?(Hash)
      unless response_metadata.nil? || response_metadata.is_a?(ResponseMetadata)
        raise InvalidArgumentError, 'call_options.response_metadata must be a SpiceDB::ResponseMetadata'
      end

      super(metadata: metadata.to_h { |key, value| [key.to_s.dup.freeze, value] }.freeze, credentials: credentials,
            response_metadata: response_metadata)
    end

    # @api private
    def stub_kwargs
      kwargs = {}
      kwargs[:metadata] = metadata.dup unless metadata.empty?
      kwargs[:credentials] = credentials if credentials
      kwargs
    end

    # @api private
    def capture? = !response_metadata.nil?
  end
end
