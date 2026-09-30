defmodule SpiceDB.Test.StandIn do
  @moduledoc """
  Starts a local gRPC server implementing whatever service modules a unit
  test needs, so tests exercise `SpiceDB`'s generated-stub call path against
  a real `GRPC.Server` instead of mocking any of SpiceDB's own modules.
  """

  @doc """
  Starts `servers` bound to a kernel-assigned loopback port and returns the
  port. Binding to port `0` lets `:ranch` pick the port atomically as part
  of the bind itself, so concurrent `async: true` tests never race over a
  "probe a free port, then rebind it" window the way `free_port/0` would.
  """
  @spec start!([module()], keyword()) :: :inet.port_number()
  def start!(servers, opts \\ []) do
    adapter_opts = [ip: {127, 0, 0, 1}] ++ Keyword.take(opts, [:cred])

    start_opts = [
      adapter_opts: adapter_opts,
      exception_log_filter: {__MODULE__, :log_exception?}
    ]

    {:ok, _pid, port} = GRPC.Server.start(servers, 0, start_opts)
    ExUnit.Callbacks.on_exit(fn -> GRPC.Server.stop(servers) end)
    port
  end

  @doc false
  @spec log_exception?(term()) :: boolean()
  def log_exception?(_exception), do: false

  @doc """
  A loopback port nothing is listening on at the time of the call, for tests
  that want a real connection failure. Not used by `start!/2`, which binds
  to port `0` instead to avoid racing other `async: true` tests over this
  port between the probe and a real listener claiming it.
  """
  @spec free_port() :: :inet.port_number()
  def free_port do
    {:ok, socket} = :gen_tcp.listen(0, ip: {127, 0, 0, 1})
    {:ok, port} = :inet.port(socket)
    :gen_tcp.close(socket)
    port
  end

  @doc """
  Starts `server` and returns a connected `SpiceDB.Client` against it, closed
  automatically at the end of the test.
  """
  @spec client!(module() | [module()], keyword()) :: SpiceDB.Client.t()
  def client!(servers, opts \\ []) do
    port = start!(List.wrap(servers), opts)
    client = SpiceDB.new_plaintext!("127.0.0.1:#{port}", "some-token")
    ExUnit.Callbacks.on_exit(fn -> SpiceDB.close(client) end)
    client
  end
end
