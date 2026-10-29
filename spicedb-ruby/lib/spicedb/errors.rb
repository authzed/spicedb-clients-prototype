# frozen_string_literal: true

require_relative 'error_details'

module SpiceDB
  # Base exception for all SpiceDB errors.
  #
  # Ruby gRPC callers rescue +GRPC::BadStatus+ and compare +#code+ with
  # +GRPC::Core::StatusCodes+. Every SpiceDB error IS a +GRPC::BadStatus+, so
  # that code keeps working unchanged, and every SpiceDB error reports its
  # matching status code: the server's code for server errors, and the class's
  # code for locally raised and caller-constructed errors (local validation is
  # INVALID_ARGUMENT, an exhausted budget is DEADLINE_EXCEEDED). See root
  # DESIGN.md, "Expose native errors without losing server diagnostics".
  #
  # Each error also matches grpc's per-code class for its code in a +rescue+
  # (or +case+) clause: +rescue GRPC::AlreadyExists+ catches an
  # AlreadyExistsError, and +rescue GRPC::Aborted+ a SpiceDB::Error whose code
  # is ABORTED. Ruby has single inheritance, so this is done by extending
  # those classes' +===+ (see GrpcCodeMatch); +is_a?(GRPC::AlreadyExists)+
  # stays false.
  #
  # +message+ is this client's display message. +server_message+ is the
  # server's status message verbatim; for an error that never reached the
  # server it is the display message, so it always has a value. +details+ (the +GRPC::BadStatus+ field) is the server message
  # when there is one and the display message otherwise. +metadata+ carries a
  # server error's trailers, so +to_rpc_status+ still decodes the rich status.
  # A mapped error keeps the original exception as +cause+.
  #
  # Beyond the message, a SpiceDB error carries the server's structured
  # explanation of the failure when the server sent one -- the
  # `google.rpc.ErrorInfo` detail attached to the status.
  #
  # @!attribute [r] reason
  #   @return [String] the name of an `authzed.api.v1.ErrorReason` enum value,
  #     e.g. `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`; empty when the server
  #     attached no ErrorInfo. Surfaced exactly as the server sent it.
  # @!attribute [r] reason_domain
  #   @return [String] who produced the reason; SpiceDB uses "authzed.com"
  # @!attribute [r] reason_metadata
  #   @return [Hash{String=>String}] the specifics behind the reason, such as
  #     which precondition failed
  # @!attribute [r] server_message
  #   @return [String] the server's status message, verbatim, or the display
  #     message for a local error
  class Error < GRPC::BadStatus
    attr_reader :delete_progress, :input_index, :reason, :reason_domain, :reason_metadata, :server_message

    # The status code an error of this class reports when none is supplied.
    def self.default_code = GRPC::Core::StatusCodes::UNKNOWN

    # Builds the error class matching +code+, for test doubles and fakes. The
    # result is indistinguishable from an error mapped from the server: it is
    # rescued by the same class and reports the same +code+.
    #
    # @param code [Integer] a GRPC::Core::StatusCodes value
    # @param message [String]
    # @return [SpiceDB::Error]
    def self.from_code(code, message, **)
      raise ArgumentError, 'code must be an Integer GRPC::Core::StatusCodes value' unless code.is_a?(Integer)

      GRPC_CODE_TO_ERROR.fetch(code, Error).new(message, code: code, **)
    end

    def initialize(message = nil, code: self.class.default_code, server_message: nil, metadata: {}, reason: '',
                   reason_domain: '', reason_metadata: {})
      display = message.nil? ? self.class.name : message.to_s
      super(code, server_message || display, metadata || {})
      @display_message = display
      @server_message = server_message || display
      @reason = reason
      @reason_domain = reason_domain
      # Copied, not aliased: the caller's Hash must not become a live handle
      # into a raised error, nor the error's into the caller's. `dup` is
      # required -- `Hash#to_h` returns `self` for a Hash.
      @reason_metadata = reason_metadata.to_h.dup.freeze
    end

    # GRPC::BadStatus formats its message as "<code>:<details>"; a SpiceDB
    # error keeps its display message.
    def to_s = @display_message

    def inspect = "#<#{self.class.name}: #{@display_message} (code=#{code})>"
  end

  # The caller does not have permission to execute the operation.
  class PermissionDeniedError < Error
    def self.default_code = GRPC::Core::StatusCodes::PERMISSION_DENIED
  end

  # The requested resource was not found.
  class NotFoundError < Error
    def self.default_code = GRPC::Core::StatusCodes::NOT_FOUND
  end

  # The resource already exists.
  class AlreadyExistsError < Error
    def self.default_code = GRPC::Core::StatusCodes::ALREADY_EXISTS
  end

  # The request contained an invalid argument: the server's INVALID_ARGUMENT,
  # or (as LocalValidationError) input this client rejected itself.
  class InvalidArgumentError < Error
    def self.default_code = GRPC::Core::StatusCodes::INVALID_ARGUMENT
  end

  # Input the client rejected locally, before sending any request: a
  # malformed tuple, an empty object type or ID, an unknown option value,
  # an endpoint it cannot dial, unrepresentable caveat context.
  #
  # It is an InvalidArgumentError (code INVALID_ARGUMENT), and +rescue
  # ArgumentError+ also catches it, the Ruby idiom around bad input (see
  # ArgumentErrorMatch; +is_a?(ArgumentError)+ stays false, since Ruby has
  # single inheritance and every SpiceDB error is a GRPC::BadStatus). An
  # INVALID_ARGUMENT from the server stays a plain InvalidArgumentError and
  # is not caught by +rescue ArgumentError+.
  class LocalValidationError < InvalidArgumentError
  end

  # Extends +ArgumentError.===+ so +rescue ArgumentError+ (and +case+) also
  # matches a LocalValidationError. Only ArgumentError itself gains this:
  # its subclasses (KeyError, a library's own ArgumentError subclass) match
  # exactly as before.
  module ArgumentErrorMatch
    ArgumentError.singleton_class.prepend(Module.new do
      define_method(:===) do |other|
        super(other) || (equal?(ArgumentError) && other.is_a?(SpiceDB::LocalValidationError))
      end
    end)
  end

  # A precondition for the operation was not met.
  class FailedPreconditionError < Error
    def self.default_code = GRPC::Core::StatusCodes::FAILED_PRECONDITION
  end

  # The service is currently unavailable.
  class UnavailableError < Error
    def self.default_code = GRPC::Core::StatusCodes::UNAVAILABLE
  end

  # The operation was cancelled.
  class CancelledError < Error
    def self.default_code = GRPC::Core::StatusCodes::CANCELLED
  end

  # The operation exceeded the deadline. An exhausted local budget raises this.
  class DeadlineExceededError < Error
    def self.default_code = GRPC::Core::StatusCodes::DEADLINE_EXCEEDED
  end

  # The server has received too many requests.
  class ResourceExhaustedError < Error
    def self.default_code = GRPC::Core::StatusCodes::RESOURCE_EXHAUSTED
  end

  # The request carried no usable credentials.
  #
  # In SpiceDB this is a wrong, expired, or rotated API token. It is distinct
  # from PermissionDeniedError, which means the caller was identified but is
  # not allowed, and from a bare Error, which may be an internal server fault.
  class UnauthenticatedError < Error
    def self.default_code = GRPC::Core::StatusCodes::UNAUTHENTICATED
  end

  # A ZedToken names a revision that is no longer available.
  #
  # SpiceDB returns OUT_OF_RANGE when the revision a ZedToken refers to has
  # expired or been garbage-collected. Recovery is mechanical: discard the
  # stale token and re-read at full consistency.
  class OutOfRangeError < Error
    def self.default_code = GRPC::Core::StatusCodes::OUT_OF_RANGE
  end

  # A client whose gRPC connection was opened in another process -- the one
  # this process forked from -- was used here. gRPC refuses any use in a
  # child once its parent has used it, so the fix is to make no SpiceDB call
  # in the parent before forking (run startup checks in Puma's
  # +on_worker_boot+): a client opens its connection on first use, so one
  # built before the fork works in each child. grpc's own fork support
  # (GRPC_ENABLE_FORK_SUPPORT=1 on Linux, with GRPC.prefork and
  # GRPC.postfork_child around the fork) also allows it. Its code is
  # FAILED_PRECONDITION.
  class ForkedClientError < Error
    def self.default_code = GRPC::Core::StatusCodes::FAILED_PRECONDITION

    # @return [Integer, nil] the process that opened the connection
    attr_reader :connected_pid

    def initialize(message = nil, connected_pid: nil, current_pid: Process.pid, **)
      @connected_pid = connected_pid
      message ||= "SpiceDB::Client opened its gRPC connection in process #{connected_pid}, and this is process #{current_pid}, " \
                  'forked from it: gRPC cannot be used in a child once its parent has used it. Make no SpiceDB call in the parent ' \
                  "before forking (run startup checks in Puma's on_worker_boot, for example): a client opens its connection on " \
                  "first use, so one built before the fork works in each child. grpc's fork support (GRPC_ENABLE_FORK_SUPPORT=1 " \
                  'on Linux, with GRPC.prefork and GRPC.postfork_child) also allows it.'
      super(message, **)
    end

    # The message for a new client in a child process whose parent (+used_pid+,
    # nil when unknown) already used gRPC.
    def self.fresh_client_message(used_pid, current_pid)
      parent = used_pid ? "process #{used_pid}" : 'a parent process'
      "spicedb: gRPC was already used in #{parent} before it forked this process (#{current_pid}), and gRPC cannot be used " \
        'in a child once its parent has used it -- building a new SpiceDB::Client in the child does not help. Make no SpiceDB ' \
        "(or other gRPC) call in the parent: connect only in each worker, for example SPICEDB.connect! in Puma's on_worker_boot. " \
        "Or enable grpc's fork support where it is available (GRPC_ENABLE_FORK_SUPPORT=1 on Linux, with GRPC.prefork and " \
        'GRPC.postfork_child around the fork).'
    end

    # Whether +error+ is grpc's own refusal to run in a forked child.
    def self.grpc_fork_refusal?(error)
      error.is_a?(RuntimeError) && error.message.match?(/before and after forking|GRPC\.prefork must be called/)
    end
  end

  # Maps gRPC status codes to SpiceDB exception classes. Codes without an
  # entry map to SpiceDB::Error, which still reports the original code.
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

  # Extends +===+ on each of grpc's per-code exception classes
  # (GRPC::AlreadyExists, GRPC::FailedPrecondition, ...) so that it also
  # matches a SpiceDB::Error with that class's status code. +rescue+ and
  # +case+ use +===+, so a handler written for the raw grpc client keeps
  # catching the native error for the same code. Nothing else about those
  # classes changes: raw grpc errors match exactly as before, and
  # +is_a?+/+ancestors+ are untouched.
  module GrpcCodeMatch
    # grpc's per-code classes and the status code each stands for.
    CLASSES = {
      GRPC::Cancelled => GRPC::Core::StatusCodes::CANCELLED,
      GRPC::Unknown => GRPC::Core::StatusCodes::UNKNOWN,
      GRPC::InvalidArgument => GRPC::Core::StatusCodes::INVALID_ARGUMENT,
      GRPC::DeadlineExceeded => GRPC::Core::StatusCodes::DEADLINE_EXCEEDED,
      GRPC::NotFound => GRPC::Core::StatusCodes::NOT_FOUND,
      GRPC::AlreadyExists => GRPC::Core::StatusCodes::ALREADY_EXISTS,
      GRPC::PermissionDenied => GRPC::Core::StatusCodes::PERMISSION_DENIED,
      GRPC::ResourceExhausted => GRPC::Core::StatusCodes::RESOURCE_EXHAUSTED,
      GRPC::FailedPrecondition => GRPC::Core::StatusCodes::FAILED_PRECONDITION,
      GRPC::Aborted => GRPC::Core::StatusCodes::ABORTED,
      GRPC::OutOfRange => GRPC::Core::StatusCodes::OUT_OF_RANGE,
      GRPC::Unimplemented => GRPC::Core::StatusCodes::UNIMPLEMENTED,
      GRPC::Internal => GRPC::Core::StatusCodes::INTERNAL,
      GRPC::Unavailable => GRPC::Core::StatusCodes::UNAVAILABLE,
      GRPC::DataLoss => GRPC::Core::StatusCodes::DATA_LOSS,
      GRPC::Unauthenticated => GRPC::Core::StatusCodes::UNAUTHENTICATED
    }.freeze

    CLASSES.each do |klass, code|
      klass.singleton_class.prepend(Module.new do
        define_method(:===) { |other| super(other) || (other.is_a?(SpiceDB::Error) && other.code == code) }
      end)
    end
  end

  # gRPC status codes the DEFAULT retry policy retries. RESOURCE_EXHAUSTED
  # (8) is deliberately excluded: in SpiceDB it signals memory load-shed or a
  # deterministic MaxDepthExceeded. A caller's RetryPolicy may opt into it.
  TRANSIENT_CODES = [
    10, # ABORTED
    14 # UNAVAILABLE
  ].freeze

  module_function

  # Converts a gRPC error to a typed SpiceDB exception. A SpiceDB error is
  # returned unchanged.
  #
  # Prefers a usable (non-empty String) `#details` — as found on
  # `GRPC::BadStatus` — over `#message`. `Google::Rpc::Status` (used for
  # per-item bulk-check errors) also responds to `#details`, but there it is
  # a repeated `google.protobuf.Any` field, so it is skipped in favor of
  # `#message`. The chosen text is kept verbatim as +server_message+.
  #
  # @param err [GRPC::BadStatus, Google::Rpc::Status] a gRPC or rich status error
  # @return [SpiceDB::Error] a typed SpiceDB exception
  def to_spicedb_error(err)
    return err if err.is_a?(Error)

    status = err.respond_to?(:code) && err.code.is_a?(Integer)
    code = status ? err.code : GRPC::Core::StatusCodes::UNKNOWN
    details = err.respond_to?(:details) ? err.details : nil
    message = if details.is_a?(String) && !details.empty?
                details
              elsif err.respond_to?(:message)
                err.message
              else
                err.to_s
              end
    metadata = err.respond_to?(:metadata) && err.metadata.is_a?(Hash) ? err.metadata : {}
    GRPC_CODE_TO_ERROR.fetch(code, Error).new(message, code: code, server_message: status ? message : nil, metadata: metadata,
                                                       **ErrorDetails.reason_kwargs(err))
  end

  # Classifies any error by its gRPC status code (a GRPC::Core::StatusCodes
  # value): a SpiceDB error, a raw GRPC::BadStatus (for example raised by a
  # fake built on the generated stubs), or a Google::Rpc::Status. nil is OK;
  # anything else is UNKNOWN.
  #
  # @param err [Exception, Google::Rpc::Status, nil]
  # @return [Integer]
  def code_of(err)
    return GRPC::Core::StatusCodes::OK if err.nil?
    return err.code if err.is_a?(GRPC::BadStatus)
    return err.code if defined?(Google::Rpc::Status) && err.is_a?(Google::Rpc::Status)

    GRPC::Core::StatusCodes::UNKNOWN
  end

  # Returns the SpiceDB error class an error classifies as, so a raw
  # GRPC::BadStatus can be tested against the same classes a native error is
  # rescued by: `SpiceDB.error_class_of(e) <= SpiceDB::FailedPreconditionError`.
  #
  # @return [Class]
  def error_class_of(err)
    return err.class if err.is_a?(Error)

    GRPC_CODE_TO_ERROR.fetch(code_of(err), Error)
  end

  # Whether grpc-ruby's fork support is enabled in this process: the
  # GRPC_ENABLE_FORK_SUPPORT environment variable is "1" on Linux, the only
  # platform grpc supports it on.
  def grpc_fork_support?
    ENV['GRPC_ENABLE_FORK_SUPPORT'] == '1' && RUBY_PLATFORM.include?('linux')
  end

  # The process that first opened a gRPC channel through this library (any
  # client), or nil. A forked child inherits it, so a child can tell that
  # its parent used gRPC, which grpc refuses to let the child do.
  def grpc_used_pid = @grpc_used_pid

  # Records that this process opened a gRPC channel (first use wins: a
  # child keeps its ancestor's pid).
  def note_grpc_use
    @grpc_used_pid = Process.pid if @grpc_used_pid.nil?
  end

  # Returns true if the DEFAULT retry policy would retry the error.
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
