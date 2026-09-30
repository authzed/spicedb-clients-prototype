defmodule SpiceDB.Streaming do
  @moduledoc false

  alias SpiceDB.{Client, Retry}

  @type spec :: %{
          required(:service) => module(),
          required(:rpc) => atom(),
          required(:request) => (term() -> struct()),
          required(:map) => (struct() -> [term()]),
          optional(:cursor) => (struct() -> term()),
          optional(:page_size) => pos_integer() | nil,
          optional(:retry) => boolean(),
          optional(:prefetch) => boolean()
        }

  @spec open(Client.t(), spec()) :: {:ok, Enumerable.t()} | {:error, SpiceDB.Error.any_error()}
  def open(%Client{} = client, spec) do
    spec =
      Map.merge(%{cursor: fn _ -> nil end, page_size: nil, retry: true, prefetch: true}, spec)

    with {:ok, first} <- establish(client, spec, nil) do
      claimed = :atomics.new(1, [])

      start = fn -> state(first_page(claimed, first, client, spec), nil) end

      {:ok,
       Stream.resource(start, &step(client, spec, &1), fn state -> release(client, state) end)}
    end
  end

  defp first_page(claimed, first, client, spec) do
    if :atomics.compare_exchange(claimed, 1, 0, 1) == :ok,
      do: first,
      else: establish!(client, spec, nil)
  end

  defp state({handle, buffer}, cursor),
    do: %{handle: handle, buffer: buffer, cursor: cursor, count: 0}

  defp step(_client, spec, %{buffer: [msg | rest]} = state),
    do: emit(spec, msg, %{state | buffer: rest})

  defp step(_client, _spec, %{handle: nil} = state), do: {:halt, state}

  defp step(client, spec, %{handle: handle} = state) do
    case client.transport.next(handle) do
      {:ok, msg, handle} ->
        emit(spec, msg, %{state | handle: handle})

      {:done, handle} ->
        if more_pages?(spec, state) do
          client.transport.cancel(handle)
          {[], state(establish!(client, spec, state.cursor), state.cursor)}
        else
          {:halt, %{state | handle: handle}}
        end

      {:error, failure, handle} ->
        client.transport.cancel(handle)
        raise Retry.normalize(failure)
    end
  end

  defp emit(spec, msg, state) do
    items = spec.map.(msg)
    cursor = spec.cursor.(msg) || state.cursor
    {items, %{state | cursor: cursor, count: state.count + length(items)}}
  end

  defp more_pages?(%{page_size: nil}, _state), do: false

  defp more_pages?(%{page_size: size}, %{count: count, cursor: cursor}),
    do: count >= size and cursor != nil

  defp release(_client, %{handle: nil}), do: :ok
  defp release(client, %{handle: handle}), do: client.transport.cancel(handle)

  defp establish!(client, spec, cursor) do
    case establish(client, spec, cursor) do
      {:ok, page} -> page
      {:error, error} -> raise error
    end
  end

  defp establish(client, spec, cursor) do
    kind = if spec.retry, do: :read, else: :mutation
    Retry.run(client, kind, fn -> open_page(client, spec, cursor) end)
  end

  defp open_page(client, spec, cursor) do
    request = spec.request.(cursor)

    with {:ok, handle} <-
           client.transport.open_stream(client.conn, spec.service, spec.rpc, request) do
      if spec.prefetch, do: prefetch(client, handle), else: {:ok, {handle, []}}
    end
  end

  defp prefetch(client, handle) do
    case client.transport.next(handle) do
      {:ok, msg, handle} ->
        {:ok, {handle, [msg]}}

      {:done, handle} ->
        {:ok, {handle, []}}

      {:error, failure, handle} ->
        client.transport.cancel(handle)
        {:error, failure}
    end
  end
end
