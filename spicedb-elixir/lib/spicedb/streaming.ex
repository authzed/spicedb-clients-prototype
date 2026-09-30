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

      {:ok, Stream.resource(start, &step(client, spec, &1), &release/1)}
    end
  end

  defp first_page(claimed, first, client, spec) do
    if :atomics.compare_exchange(claimed, 1, 0, 1) == :ok,
      do: first,
      else: establish!(client, spec, nil)
  end

  defp state({puller, buffer}, cursor),
    do: %{puller: puller, buffer: buffer, cursor: cursor, count: 0}

  defp step(_client, spec, %{buffer: [msg | rest]} = state),
    do: emit(spec, msg, %{state | buffer: rest})

  defp step(_client, _spec, %{puller: nil} = state), do: {:halt, state}

  defp step(client, spec, %{puller: puller} = state) do
    case pull(puller) do
      {:item, {:ok, msg}, puller} ->
        emit(spec, msg, %{state | puller: puller})

      :done ->
        if more_pages?(spec, state) do
          {[], state(establish!(client, spec, state.cursor), state.cursor)}
        else
          {:halt, %{state | puller: nil}}
        end

      {:item, {:error, failure}, _puller} ->
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

  # No handle exists to cancel a server-streaming call; see DESIGN.md's "Stream lifecycle" section.
  defp release(_state), do: :ok

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

    with {:ok, enumerable} <- call_stream(client.conn, spec.service, spec.rpc, request) do
      puller = {:cont, enumerable}
      if spec.prefetch, do: prefetch(puller), else: {:ok, {puller, []}}
    end
  end

  defp call_stream(conn, service, rpc, request) do
    apply(stub_module(service), stub_function(rpc), [conn, request, []])
  end

  defp prefetch(puller) do
    case pull(puller) do
      :done -> {:ok, {nil, []}}
      {:item, {:ok, msg}, puller} -> {:ok, {puller, [msg]}}
      {:item, {:error, failure}, _puller} -> {:error, failure}
    end
  end

  defp pull({:cont, enumerable}), do: pull_result(reduce_step(enumerable))
  defp pull({:suspended, continuation}), do: pull_result(continuation.({:cont, []}))

  defp pull_result({:done, []}), do: :done

  defp pull_result({:suspended, [item], continuation}),
    do: {:item, item, {:suspended, continuation}}

  defp reduce_step(enumerable),
    do: Enumerable.reduce(enumerable, {:cont, []}, fn item, _acc -> {:suspend, [item]} end)

  defp stub_module(service) do
    service
    |> Module.split()
    |> List.replace_at(-1, "Stub")
    |> Module.concat()
  end

  defp stub_function(rpc) do
    rpc
    |> Atom.to_string()
    |> Macro.underscore()
    |> String.to_existing_atom()
  end
end
