# frozen_string_literal: true

require_relative 'error_details'

module SpiceDB
  # Base exception for all SpiceDB errors.
  #
  # Beyond the message, a SpiceDB error carries the server's structured
  # explanation of the failure when the server sent one -- the
  # `google.rpc.ErrorInfo` detail attached to the status. See root DESIGN.md,
  # "RULE: Error mapping must not lose the server's detail".
  #
  # @!attribute [r] reason
  #   @return [String] the name of an `authzed.api.v1.ErrorReason` enum value,
  #     e.g. `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`; empty when the server
  #     attached no ErrorInfo. Surfaced exactly as the server sent it -- a
  #     reason a newer server knows and this client does not is passed through
  #     unchanged rather than coerced or rejected, because it is
  #     server-supplied and root DESIGN.md's "RULE: A conversion that cannot
  #     preserve meaning must fail" requires server-supplied unknowns to
  #     degrade safely rather than raise.
  # @!attribute [r] reason_domain
  #   @return [String] who produced the reason; SpiceDB uses "authzed.com"
  # @!attribute [r] reason_metadata
  #   @return [Hash{String=>String}] the specifics behind the reason, such as
  #     which precondition failed
  class Error < StandardError
    attr_reader :reason, :reason_domain, :reason_metadata

    def initialize(message = nil, reason: '', reason_domain: '', reason_metadata: {})
      super(message)
      @reason = reason
      @reason_domain = reason_domain
      # Copied, not aliased: the caller's Hash must not become a live handle
      # into a raised error, nor the error's into the caller's. Every other
      # client copies here. `dup` is required -- `Hash#to_h` returns `self` for
      # a Hash, so freezing its result alone would freeze the caller's object.
      @reason_metadata = reason_metadata.to_h.dup.freeze
    end
  end

  # The caller does not have permission to execute the operation.
  class PermissionDeniedError < Error; end

  # The requested resource was not found.
  class NotFoundError < Error; end

  # The resource already exists.
  class AlreadyExistsError < Error; end

  # The request contained an invalid argument.
  class InvalidArgumentError < Error; end

  # A precondition for the operation was not met.
  class FailedPreconditionError < Error; end

  # The service is currently unavailable.
  class UnavailableError < Error; end

  # The operation was cancelled.
  class CancelledError < Error; end

  # The operation exceeded the deadline.
  class DeadlineExceededError < Error; end

  # The server has received too many requests.
  class ResourceExhaustedError < Error; end

  # The request carried no usable credentials.
  #
  # In SpiceDB this is a wrong, expired, or rotated API token -- the most
  # common error a new integration produces. It is distinct from
  # PermissionDeniedError, which means the caller was identified but is not
  # allowed, and from a bare Error, which may be an internal server fault:
  # refresh credentials on this one, page someone on that one.
  class UnauthenticatedError < Error; end

  # A ZedToken names a revision that is no longer available.
  #
  # SpiceDB returns OUT_OF_RANGE when the revision a ZedToken refers to has
  # expired or been garbage-collected. Recovery is mechanical: discard the
  # stale token and re-read at full consistency.
  class OutOfRangeError < Error; end

  # Maps gRPC status codes to SpiceDB exception classes.
  #
  # Uses GRPC::Core::StatusCodes constants when the grpc gem is available,
  # falling back to integer codes.
  GRPC_CODE_TO_ERROR = {
    1 => CancelledError,          # CANCELLED
    3 => InvalidArgumentError,    # INVALID_ARGUMENT
    4 => DeadlineExceededError,   # DEADLINE_EXCEEDED
    5 => NotFoundError,           # NOT_FOUND
    6 => AlreadyExistsError,      # ALREADY_EXISTS
    7 => PermissionDeniedError,   # PERMISSION_DENIED
    8 => ResourceExhaustedError,  # RESOURCE_EXHAUSTED
    9 => FailedPreconditionError, # FAILED_PRECONDITION
    11 => OutOfRangeError,        # OUT_OF_RANGE
    14 => UnavailableError,       # UNAVAILABLE
    16 => UnauthenticatedError    # UNAUTHENTICATED
  }.freeze

  # gRPC status codes that are transient and worth retrying.
  #
  # RESOURCE_EXHAUSTED (8) is deliberately excluded. In SpiceDB it signals
  # either memory load-shed (retrying adds load to an already-overloaded
  # server) or a deterministic MaxDepthExceeded (retrying can never succeed
  # -- it just re-runs the most expensive class of check several times
  # before surfacing the same error). See DESIGN.md, "Automatic retry is
  # for idempotent operations only".
  TRANSIENT_CODES = [
    10, # ABORTED
    14 # UNAVAILABLE
  ].freeze

  module_function

  # Converts a gRPC error to a typed SpiceDB exception.
  #
  # Prefers a usable (non-empty String) `#details` — as found on
  # `GRPC::BadStatus` — over `#message`. `Google::Rpc::Status` (used for
  # per-item bulk-check errors) also responds to `#details`, but there it is
  # a repeated `google.protobuf.Any` field, not a message string, so it is
  # skipped in favor of `#message`.
  #
  # The server's ErrorInfo detail, when present, is surfaced on the returned
  # exception as `reason`/`reason_domain`/`reason_metadata`. See root
  # DESIGN.md, "RULE: Error mapping must not lose the server's detail".
  #
  # @param err [GRPC::BadStatus, Google::Rpc::Status] a gRPC or rich status error
  # @return [SpiceDB::Error] a typed SpiceDB exception
  def to_spicedb_error(err)
    code = err.respond_to?(:code) ? err.code : nil
    cls = GRPC_CODE_TO_ERROR.fetch(code, Error)
    details = err.respond_to?(:details) ? err.details : nil
    message = if details.is_a?(String) && !details.empty?
                details
              elsif err.respond_to?(:message)
                err.message
              else
                err.to_s
              end
    cls.new(message, **ErrorDetails.reason_kwargs(err))
  end

  # Returns true if the error is transient and worth retrying.
  #
  # @param err [Exception]
  # @return [Boolean]
  def transient?(err)
    if err.respond_to?(:code)
      TRANSIENT_CODES.include?(err.code)
    else
      err.is_a?(UnavailableError)
    end
  end
end
