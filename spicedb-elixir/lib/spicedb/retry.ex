defmodule SpiceDB.Retry do
  @moduledoc false

  alias SpiceDB.Client

  @type kind :: :read | :mutation

  # grpc 1.0.5's Mint adapter collapses any transport-level failure (e.g. a
  # connection reset mid-handshake) into this exact message prefix on a
  # GRPC.RPCError with status `unknown`, discarding the original error's
  # structure. See deps/grpc/lib/grpc/client/adapters/mint.ex,
  # `handle_errors_receive_data/2`.
  @transport_failure_prefix "error occurred while receiving data: "
  @unknown_status GRPC.Status.unknown()

  @spec run(Client.t(), kind(), (-> {:ok, result} | {:error, term()})) ::
          {:ok, result} | {:error, SpiceDB.Error.any_error()}
        when result: term()
  def run(%Client{} = client, kind, fun), do: attempt(client, kind, fun, 1)

  defp attempt(client, kind, fun, n) do
    case fun.() do
      {:ok, _result} = ok ->
        ok

      {:error, failure} ->
        error = normalize(failure)

        if kind == :read and retryable?(error) and n <= client.max_retries do
          Process.sleep(backoff(client, n))
          attempt(client, kind, fun, n + 1)
        else
          {:error, error}
        end
    end
  end

  @spec backoff(Client.t(), pos_integer()) :: non_neg_integer()
  def backoff(%Client{retry_base_ms: base}, attempt),
    do: round(:rand.uniform() * base * Integer.pow(2, attempt - 1))

  @spec retryable?(Exception.t()) :: boolean()
  def retryable?(%SpiceDB.UnavailableError{}), do: true
  def retryable?(%SpiceDB.Error{code: 10}), do: true
  def retryable?(_error), do: false

  @spec normalize(term()) :: SpiceDB.Error.any_error()
  def normalize(%GRPC.RPCError{status: @unknown_status, message: message})
      when byte_size(message) >= byte_size(@transport_failure_prefix) and
             binary_part(message, 0, byte_size(@transport_failure_prefix)) ==
               @transport_failure_prefix do
    %SpiceDB.UnavailableError{message: message}
  end

  def normalize(%GRPC.RPCError{} = error), do: SpiceDB.Error.from_grpc_status(error)
  def normalize(%Google.Rpc.Status{} = status), do: SpiceDB.Error.from_grpc_status(status)

  def normalize(:deadline_exceeded),
    do: %SpiceDB.DeadlineExceededError{message: "deadline exceeded", code: 4}

  def normalize(%module{} = error) do
    if module in SpiceDB.Error.kinds(), do: error, else: unavailable(error)
  end

  def normalize(other), do: unavailable(other)

  defp unavailable(%{__exception__: true} = error),
    do: %SpiceDB.UnavailableError{message: "transport failure: " <> Exception.message(error)}

  defp unavailable(other),
    do: %SpiceDB.UnavailableError{message: "transport failure: " <> inspect(other)}
end
