defmodule SpiceDB.Examples.StandIn do
  @moduledoc """
  Starts a local gRPC server implementing whatever service modules an example
  needs, for behavior a real SpiceDB cannot be made to produce on demand
  (a given status code, a fixed number of `UNAVAILABLE`s, a TLS endpoint).
  """

  @doc """
  Starts `servers` on a free loopback port under the test supervisor and
  returns the port. Pass `cred: GRPC.Credential.new(ssl: [...])` for TLS.
  """
  @spec start!([module()], keyword()) :: :inet.port_number()
  def start!(servers, opts \\ []) do
    port = free_port()
    adapter_opts = [ip: {127, 0, 0, 1}] ++ Keyword.take(opts, [:cred])

    spec =
      Supervisor.child_spec(
        {GRPC.Server.Supervisor,
         servers: servers,
         port: port,
         start_server: true,
         adapter_opts: adapter_opts,
         exception_log_filter: {__MODULE__, :log_exception?}},
        id: make_ref()
      )

    ExUnit.Callbacks.start_supervised!(spec)
    port
  end

  @doc false
  @spec log_exception?(term()) :: boolean()
  def log_exception?(_exception), do: false

  @doc "A loopback port nothing is listening on at the time of the call."
  @spec free_port() :: :inet.port_number()
  def free_port do
    {:ok, socket} = :gen_tcp.listen(0, ip: {127, 0, 0, 1})
    {:ok, port} = :inet.port(socket)
    :gen_tcp.close(socket)
    port
  end
end
