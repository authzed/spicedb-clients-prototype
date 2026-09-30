defmodule SpiceDB.Test.FakeTransport do
  @moduledoc """
  A `SpiceDB.Transport` that answers from a handler function instead of a
  network. Every request is also sent to the test process, so a test can
  assert on exactly what went over the wire and how many times.

  The handler receives `(rpc, request)` and returns:

    * `{:ok, response}` or `{:error, failure}` for a unary or client-streaming call
    * `{:stream, items}` for a server stream, where each item is a response
      message or `{:error, failure}`; the stream ends after the last item
  """

  @behaviour SpiceDB.Transport

  @type handler :: (atom(), struct() -> term())

  @spec client((atom(), struct() -> term()), keyword()) :: SpiceDB.Client.t()
  def client(handler, opts \\ []) do
    SpiceDB.new_with_transport(
      __MODULE__,
      %{pid: self(), handler: handler},
      Keyword.merge([retry_base_ms: 0], opts)
    )
  end

  @impl true
  def unary(%{pid: pid, handler: handler}, _service, rpc, request, opts) do
    send(pid, {:rpc, rpc, request, opts})
    handler.(rpc, request)
  end

  @impl true
  def open_stream(%{pid: pid, handler: handler}, _service, rpc, request) do
    send(pid, {:open_stream, rpc, request})

    case handler.(rpc, request) do
      {:stream, items} -> {:ok, %{pid: pid, rpc: rpc, items: items, id: make_ref()}}
      {:error, failure} -> {:error, failure}
    end
  end

  @impl true
  def next(%{items: []} = handle), do: {:done, handle}

  def next(%{items: [{:error, failure} | rest]} = handle),
    do: {:error, failure, %{handle | items: rest}}

  def next(%{items: [msg | rest]} = handle), do: {:ok, msg, %{handle | items: rest}}

  @impl true
  def cancel(%{pid: pid, rpc: rpc, id: id}) do
    send(pid, {:cancel, rpc, id})
    :ok
  end

  @impl true
  def client_stream(%{pid: pid, handler: handler}, _service, rpc, requests, opts) do
    requests = Enum.to_list(requests)
    send(pid, {:client_stream, rpc, requests, opts})
    handler.(rpc, requests)
  end
end
