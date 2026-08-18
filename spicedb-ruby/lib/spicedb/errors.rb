# frozen_string_literal: true

module SpiceDB
  # Base exception for all SpiceDB errors.
  class Error < StandardError; end

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
    14 => UnavailableError        # UNAVAILABLE
  }.freeze

  # gRPC status codes that are transient and worth retrying.
  TRANSIENT_CODES = [
    8,  # RESOURCE_EXHAUSTED
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
    cls.new(message)
  end

  # Returns true if the error is transient and worth retrying.
  #
  # @param err [Exception]
  # @return [Boolean]
  def transient?(err)
    if err.respond_to?(:code)
      TRANSIENT_CODES.include?(err.code)
    else
      err.is_a?(UnavailableError) ||
        err.is_a?(ResourceExhaustedError)
    end
  end
end
