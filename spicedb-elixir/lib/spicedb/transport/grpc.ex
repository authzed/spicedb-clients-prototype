defmodule SpiceDB.Transport.GRPC do
  @moduledoc false

  @behaviour SpiceDB.Transport

  alias GRPC.Client.Connection
  alias GRPC.Client.Stream, as: ClientStream

  @impl true
  def unary(%GRPC.Channel{} = channel, service, rpc, request, opts) do
    apply(stub(service), function_name(rpc), [channel, request, opts])
  end

  # GRPC.Stub's server-streaming call hands back only an enumerable and drops
  # the GRPC.Client.Stream, which is the one value GRPC.Stub.cancel/1 needs.
  # So the stream is assembled here the way GRPC.Stub.call/5 assembles it,
  # keeping hold of it. Channel interceptors and client telemetry spans are
  # not applied to server streams as a result.
  @impl true
  def open_stream(%GRPC.Channel{} = channel, service, rpc, request) do
    ch = pick_channel(channel)
    {_name, {req_mod, _}, {res_mod, _}, _options} = rpc_tuple = rpc_tuple(service, rpc)
    service_name = service.__meta__(:name)

    stream = %ClientStream{
      channel: ch,
      service_name: service_name,
      method_name: to_string(rpc),
      grpc_type: GRPC.Service.grpc_type(rpc_tuple),
      path: "/#{service_name}/#{rpc}",
      rpc: rpc_tuple,
      server_stream: true,
      request_mod: req_mod,
      response_mod: res_mod,
      codec: ch.codec,
      compressor: ch.compressor,
      accepted_compressors: ch.accepted_compressors
    }

    stream =
      ch.adapter.send_request(stream, ch.codec.encode(request),
        timeout: :infinity,
        compressor: ch.compressor
      )

    case stream.payload do
      %{response: {:ok, %{request_ref: _}}} ->
        {:ok, %{stream: stream, replies: nil, done: false}}

      %{response: failure} = payload ->
        stop_response_process(payload)
        {:error, failure}
    end
  rescue
    e in [ArgumentError, RuntimeError] -> {:error, e}
  end

  @impl true
  def next(%{done: true} = handle), do: {:done, handle}

  def next(%{replies: nil, stream: stream} = handle) do
    case GRPC.Stub.recv(stream) do
      {:ok, replies} -> next(%{handle | replies: replies})
      {:error, error} -> {:error, error, %{handle | done: true}}
    end
  end

  def next(%{replies: replies} = handle) do
    case Enum.take(replies, 1) do
      [{:ok, reply}] -> {:ok, reply, handle}
      [{:error, error}] -> {:error, error, %{handle | done: true}}
      [] -> {:done, %{handle | done: true}}
    end
  end

  @impl true
  def cancel(%{done: true}), do: :ok

  def cancel(%{stream: stream}) do
    try do
      GRPC.Stub.cancel(stream)
    catch
      :exit, _reason -> :ok
    end

    stop_response_process(stream.payload)
  end

  @impl true
  def client_stream(%GRPC.Channel{} = channel, service, rpc, requests, opts) do
    {timeout, stub_opts} = Keyword.pop(opts, :timeout, :infinity)
    deadline = deadline(timeout)
    stub_opts = if timeout == :infinity, do: stub_opts, else: [{:timeout, timeout} | stub_opts]
    stream = apply(stub(service), function_name(rpc), [channel, stub_opts])

    try do
      Enum.each(requests, &GRPC.Stub.send_request(stream, &1))
      GRPC.Stub.end_stream(stream)
    rescue
      # The Mint adapter asserts `:ok` on every body write, so a server that
      # resets the stream mid-import surfaces as a MatchError. The stream's
      # status, read below, says why.
      MatchError ->
        :reset

      e ->
        cancel(%{stream: stream})
        reraise e, __STACKTRACE__
    end

    await_client_stream(stream, deadline)
  end

  defp await_client_stream(stream, :infinity), do: GRPC.Stub.recv(stream)

  defp await_client_stream(stream, deadline) do
    task = Task.async(fn -> GRPC.Stub.recv(stream) end)
    remaining = max(deadline - System.monotonic_time(:millisecond), 0)

    case Task.yield(task, remaining) || Task.shutdown(task, :brutal_kill) do
      {:ok, result} ->
        result

      _timed_out ->
        cancel(%{stream: stream})

        {:error,
         GRPC.RPCError.exception(GRPC.Status.deadline_exceeded(), "client-side deadline exceeded")}
    end
  end

  defp deadline(:infinity), do: :infinity
  defp deadline(ms) when is_integer(ms), do: System.monotonic_time(:millisecond) + ms

  defp stop_response_process(%{stream_response_pid: pid}) when is_pid(pid) do
    GenServer.stop(pid, :normal)
  catch
    :exit, _reason -> :ok
  end

  defp stop_response_process(_payload), do: :ok

  defp pick_channel(channel) do
    case Connection.pick_channel(channel) do
      {:ok, %GRPC.Channel{} = ch} -> ch
      _no_pick -> channel
    end
  end

  defp rpc_tuple(service, rpc), do: List.keyfind!(service.__rpc_calls__(), rpc, 0)

  defp stub(service) do
    service
    |> Module.split()
    |> List.replace_at(-1, "Stub")
    |> Module.concat()
  end

  defp function_name(rpc) do
    rpc
    |> Atom.to_string()
    |> Macro.underscore()
    |> String.to_existing_atom()
  end
end
