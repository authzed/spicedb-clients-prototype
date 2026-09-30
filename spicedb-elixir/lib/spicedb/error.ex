defmodule SpiceDB.Error do
  @moduledoc """
  Base error for every failure this client reports, and the fallback for a
  gRPC status that has no dedicated kind.

  Each status kind is its own exception module (`SpiceDB.NotFoundError`,
  `SpiceDB.PermissionDeniedError`, ...) with the same fields as this one:

    * `:message` - the server's status message
    * `:code` - the original gRPC status code as an integer (`nil` when the
      failure did not come from a gRPC status)
    * `:reason` - `google.rpc.ErrorInfo.reason`, e.g.
      `"ERROR_REASON_SCHEMA_PARSE_ERROR"`, or `""` when the server sent none
    * `:reason_domain` - `google.rpc.ErrorInfo.domain`, or `""`
    * `:reason_metadata` - `google.rpc.ErrorInfo.metadata` as a map of
      strings, or `%{}`

  Elixir exceptions have no inheritance, so there is no shared parent type to
  rescue. Rescue several kinds with a list:

      try do
        SpiceDB.check_permission!(client, consistency, "view", relationship)
      rescue
        e in [SpiceDB.UnavailableError, SpiceDB.DeadlineExceededError] -> retry_later(e)
      end

  or match on the struct in a non-raising call:

      case SpiceDB.check_permission(client, consistency, "view", relationship) do
        {:ok, result} -> result
        {:error, %SpiceDB.NotFoundError{}} -> nil
      end

  `kinds/0` lists every module, for a caller that wants to rescue any error
  this client raises.
  """

  @fields [message: "", code: nil, reason: "", reason_domain: "", reason_metadata: %{}]

  defexception @fields

  @type t :: %__MODULE__{
          message: String.t(),
          code: non_neg_integer() | nil,
          reason: String.t(),
          reason_domain: String.t(),
          reason_metadata: %{optional(String.t()) => String.t()}
        }

  @typedoc "Any exception struct this client returns or raises."
  @type any_error ::
          t()
          | SpiceDB.CancelledError.t()
          | SpiceDB.InvalidArgumentError.t()
          | SpiceDB.DeadlineExceededError.t()
          | SpiceDB.NotFoundError.t()
          | SpiceDB.AlreadyExistsError.t()
          | SpiceDB.PermissionDeniedError.t()
          | SpiceDB.ResourceExhaustedError.t()
          | SpiceDB.FailedPreconditionError.t()
          | SpiceDB.OutOfRangeError.t()
          | SpiceDB.UnavailableError.t()
          | SpiceDB.UnauthenticatedError.t()

  @typep rpc_error :: %GRPC.RPCError{}

  @kinds %{
    1 => SpiceDB.CancelledError,
    3 => SpiceDB.InvalidArgumentError,
    4 => SpiceDB.DeadlineExceededError,
    5 => SpiceDB.NotFoundError,
    6 => SpiceDB.AlreadyExistsError,
    7 => SpiceDB.PermissionDeniedError,
    8 => SpiceDB.ResourceExhaustedError,
    9 => SpiceDB.FailedPreconditionError,
    11 => SpiceDB.OutOfRangeError,
    14 => SpiceDB.UnavailableError,
    16 => SpiceDB.UnauthenticatedError
  }

  @doc false
  @spec fields() :: keyword()
  def fields, do: @fields

  @doc "Every exception module this client can return or raise."
  @spec kinds() :: [module()]
  def kinds, do: [__MODULE__ | Map.values(@kinds)]

  @doc """
  Maps a gRPC status to its typed error.

  Accepts a `GRPC.RPCError` (what grpc-elixir returns) or a
  `Google.Rpc.Status` (what a bulk check carries per item). A status code
  with no dedicated kind maps to `SpiceDB.Error` itself, keeping the code.
  """
  @spec from_grpc_status(rpc_error() | Google.Rpc.Status.t()) :: any_error()
  def from_grpc_status(%GRPC.RPCError{status: code, message: message, details: details}) do
    build(code, message, details)
  end

  def from_grpc_status(%Google.Rpc.Status{code: code, message: message, details: details}) do
    build(code, message, details)
  end

  defp build(code, message, details) do
    module = Map.get(@kinds, code, __MODULE__)
    info = SpiceDB.ErrorDetails.error_info(details)

    struct(module,
      message: message || "",
      code: code,
      reason: info.reason,
      reason_domain: info.domain,
      reason_metadata: info.metadata
    )
  end
end

defmodule SpiceDB.ErrorDetails do
  @moduledoc false

  alias Google.Protobuf.Any
  alias Google.Rpc.ErrorInfo

  @error_info_type "type.googleapis.com/google.rpc.ErrorInfo"

  @spec error_info([Any.t()] | nil) :: %{
          reason: String.t(),
          domain: String.t(),
          metadata: %{optional(String.t()) => String.t()}
        }
  def error_info(details) when is_list(details) do
    Enum.find_value(details, empty(), fn
      %Any{type_url: @error_info_type, value: value} -> decode(value)
      _other -> nil
    end)
  end

  def error_info(_details), do: empty()

  defp decode(value) do
    info = ErrorInfo.decode(value)
    %{reason: info.reason, domain: info.domain, metadata: Map.new(info.metadata)}
  rescue
    _malformed -> nil
  end

  defp empty, do: %{reason: "", domain: "", metadata: %{}}
end

for {name, doc} <- [
      {CancelledError, "The call was cancelled (`CANCELLED`, code 1)."},
      {InvalidArgumentError,
       "The request was malformed (`INVALID_ARGUMENT`, code 3), or a caller argument was rejected before any request was sent."},
      {DeadlineExceededError, "The call's deadline elapsed (`DEADLINE_EXCEEDED`, code 4)."},
      {NotFoundError, "A named object does not exist (`NOT_FOUND`, code 5)."},
      {AlreadyExistsError,
       "The relationship or object already exists (`ALREADY_EXISTS`, code 6)."},
      {PermissionDeniedError,
       "The token may not perform this call (`PERMISSION_DENIED`, code 7)."},
      {ResourceExhaustedError,
       "The server shed load or hit a depth limit (`RESOURCE_EXHAUSTED`, code 8). Never retried."},
      {FailedPreconditionError,
       "A precondition or schema requirement failed (`FAILED_PRECONDITION`, code 9)."},
      {OutOfRangeError,
       "A revision or cursor is outside what the server can serve (`OUT_OF_RANGE`, code 11), e.g. a ZedToken older than the GC window."},
      {UnavailableError,
       "The server could not be reached (`UNAVAILABLE`, code 14), or the transport failed before a status arrived."},
      {UnauthenticatedError, "The token is missing or invalid (`UNAUTHENTICATED`, code 16)."}
    ] do
  module = Module.concat(SpiceDB, name)

  defmodule module do
    @moduledoc doc <> "\n\nFields are documented on `SpiceDB.Error`."
    defexception SpiceDB.Error.fields()

    @type t :: %__MODULE__{
            message: String.t(),
            code: non_neg_integer() | nil,
            reason: String.t(),
            reason_domain: String.t(),
            reason_metadata: %{optional(String.t()) => String.t()}
          }
  end
end
