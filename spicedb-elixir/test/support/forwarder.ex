defmodule SpiceDB.Test.Forwarder do
  @moduledoc """
  Starts a real `GRPC.Server`, generated on the fly for whatever RPCs a test
  needs, that forwards every call to a plain handler function. This is what
  replaces the deleted `SpiceDB.Test.FakeTransport`: tests get the same
  handler-function ergonomics they had before, but `SpiceDB` still talks to
  a real `GRPC.Server.Supervisor` over loopback, never a mock of `SpiceDB`'s
  own modules.

  A fresh module is generated per call (via `Module.create/3`) so tests can
  stay `async: true` without sharing state through a fixed module name.
  """

  alias SpiceDB.Test.StandIn

  @typedoc "`:unary` and `:client_stream` reply like a fake transport would; `:server_stream` yields a canned list."
  @type kind :: :unary | :server_stream | :client_stream

  @doc """
  Starts a server for `service` implementing `rpcs` (a map of RPC name atom
  to `kind/0`) and returns a connected `SpiceDB.Client`.

  `handler` is `(rpc_name, request) -> result`, where `request` is the
  decoded request for `:unary`/`:server_stream`, or the list of every
  decoded request received for `:client_stream`, and `result` is:

    * `:unary`, `:client_stream` - `{:ok, reply} | {:error, failure}`
    * `:server_stream` - `{:stream, [reply | {:error, failure}]}`, sent in
      order; an `{:error, failure}` element ends the stream at that point

  Every call is also relayed to the calling test process so it can
  `assert_received`. The trailing `deadline_ms` is the caller's remaining
  per-attempt deadline decoded from the `grpc-timeout` header (`nil` when
  the caller set none), the one piece of client-side timeout intent that is
  actually observable from the server side:

    * `:unary` - `{:rpc, rpc_name, request, deadline_ms}`
    * `:server_stream` - `{:open_stream, rpc_name, request}`
    * `:client_stream` - `{:client_stream, rpc_name, requests, deadline_ms}`
  """
  @spec client!(module(), %{atom() => kind()}, (atom(), term() -> term())) :: SpiceDB.Client.t()
  def client!(service, rpcs, handler) do
    {:ok, agent} = Agent.start_link(fn -> handler end)
    ExUnit.Callbacks.on_exit(fn -> if Process.alive?(agent), do: Agent.stop(agent) end)

    service
    |> define!(rpcs, agent, self())
    |> StandIn.client!()
  end

  defp define!(service, rpcs, agent, test) do
    name = Module.concat(__MODULE__, "T#{System.unique_integer([:positive])}")
    defs = Enum.map(rpcs, &rpc_def(&1, agent, test))

    contents =
      quote do
        use GRPC.Server, service: unquote(service)
        unquote_splicing(defs)
      end

    {:module, module, _binary, _term} =
      Module.create(name, contents, file: __ENV__.file, line: __ENV__.line)

    module
  end

  defp rpc_def({rpc, :unary}, agent, test) do
    quote do
      def unquote(snake(rpc))(request, stream) do
        unquote(__MODULE__).unary(unquote(agent), unquote(test), unquote(rpc), request, stream)
      end
    end
  end

  defp rpc_def({rpc, :server_stream}, agent, test) do
    quote do
      def unquote(snake(rpc))(request, stream) do
        unquote(__MODULE__).server_stream(
          unquote(agent),
          unquote(test),
          unquote(rpc),
          request,
          stream
        )
      end
    end
  end

  defp rpc_def({rpc, :client_stream}, agent, test) do
    quote do
      def unquote(snake(rpc))(requests, stream) do
        unquote(__MODULE__).client_stream(
          unquote(agent),
          unquote(test),
          unquote(rpc),
          requests,
          stream
        )
      end
    end
  end

  defp snake(rpc), do: rpc |> Atom.to_string() |> Macro.underscore() |> String.to_atom()

  @doc false
  def unary(agent, test, rpc, request, stream) do
    send(test, {:rpc, rpc, request, deadline_ms(stream)})

    case Agent.get(agent, & &1.(rpc, request)) do
      {:ok, reply} -> reply
      {:error, failure} -> raise failure
    end
  end

  @doc false
  def server_stream(agent, test, rpc, request, stream) do
    send(test, {:open_stream, rpc, request})

    # A real streaming handler accepts the call and sends response headers up
    # front, then may still fail partway through; model that here so a canned
    # error is observed the same way a genuine mid-stream rejection would be,
    # rather than as a call-time failure that is an artifact of this double.
    GRPC.Server.send_headers(stream, %{})

    case Agent.get(agent, & &1.(rpc, request)) do
      {:stream, items} ->
        Enum.each(items, fn
          {:error, failure} -> raise failure
          item -> GRPC.Server.send_reply(stream, item)
        end)

      {:error, failure} ->
        raise failure
    end
  end

  @doc false
  def client_stream(agent, test, rpc, request_stream, stream) do
    requests = Enum.to_list(request_stream)
    send(test, {:client_stream, rpc, requests, deadline_ms(stream)})

    case Agent.get(agent, & &1.(rpc, requests)) do
      {:ok, reply} -> reply
      {:error, failure} -> raise failure
    end
  end

  defp deadline_ms(%{deadline: nil}), do: nil

  defp deadline_ms(%{deadline: deadline}),
    do: max(deadline - System.monotonic_time(:millisecond), 0)
end
