defmodule SpicedbProto.ClientTest do
  use ExUnit.Case, async: true

  alias SpicedbProto.Client
  alias SpicedbProto.InsecureRemoteHostError

  # Authority-shifting targets: endpoints whose URI authority is not what a naive
  # host:port split reads out of them. This exact set defeated the equivalent guard
  # in this repo's C#, Rust, TypeScript and Java clients -- a last-colon (or
  # first-"]") split read a loopback host out of them, while those transports parsed
  # the same string as a URI, took "127.0.0.1:443" for userinfo, and connected to
  # evil.com, shipping the bearer token there in cleartext. grpc-elixir 1.0.5 was
  # verified not exploitable by "127.0.0.1:443@evil.com" (it raises ArgumentError
  # parsing the port and never contacts evil.com), but the guard must fail closed on
  # a target it cannot vouch for, and this fixture is what would catch a future edit
  # that loosened the split here the way the C# one was loosened.
  @authority_shifting_endpoints [
    "127.0.0.1:443@evil.com",
    "[::1]:443@evil.com",
    "[::1]:0@127.0.0.1:19999",
    "[localhost]:1@127.0.0.1:19999",
    "localhost@evil.com",
    "localhost/../evil.com",
    "localhost#@evil.com",
    "localhost?@evil.com",
    "localhost.",
    "localhost :50051",
    "127.0.0.1 :50051",
    "127.0.0.1:notaport"
  ]

  describe "loopback_endpoint?/1" do
    loopback = [
      "localhost:50051",
      "LOCALHOST:50051",
      "localhost",
      "127.0.0.1:50051",
      "127.0.0.1",
      "127.55.66.77:50051",
      "[::1]:50051",
      "::1",
      "unix:/var/run/spicedb.sock",
      "unix:///var/run/spicedb.sock",
      "UNIX:/var/run/spicedb.sock",
      "Unix:///var/run/spicedb.sock"
    ]

    for endpoint <- loopback do
      test "treats #{inspect(endpoint)} as loopback" do
        assert Client.loopback_endpoint?(unquote(endpoint))
      end
    end

    not_loopback = [
      "example.com:443",
      "staging.internal:443",
      "10.0.0.5:50051",
      "8.8.8.8:443",
      "0.0.0.0:50051",
      "localhost.evil.com:443",
      "127.0.0.1.evil.com:443",
      "evil-localhost:443"
    ]

    for endpoint <- not_loopback do
      test "does not treat #{inspect(endpoint)} as loopback" do
        refute Client.loopback_endpoint?(unquote(endpoint))
      end
    end

    for endpoint <- @authority_shifting_endpoints do
      test "does not treat authority-shifting #{inspect(endpoint)} as loopback" do
        refute Client.loopback_endpoint?(unquote(endpoint))
      end
    end
  end

  describe "insecure host guard" do
    test "refuses a non-loopback endpoint without the opt-in" do
      assert_raise InsecureRemoteHostError, ~r/evil\.example\.com:1234/, fn ->
        Client.connect("evil.example.com:1234", "super-secret-token", insecure: true)
      end
    end

    test "names the opt-in in the error message" do
      assert_raise InsecureRemoteHostError, ~r/allow_insecure_remote_credentials/, fn ->
        Client.connect("evil.example.com:1234", "super-secret-token", insecure: true)
      end
    end

    test "allows a loopback endpoint with no opt-in, and actually carries the token" do
      with_tcp_listener(fn port ->
        {:ok, client} = Client.connect("localhost:#{port}", "test-token", insecure: true)
        assert client.channel.headers == %{"authorization" => "Bearer test-token"}
      end)
    end

    test "allows a non-loopback endpoint when allow_insecure_remote_credentials is true, and sends the token" do
      with_tcp_listener(fn port ->
        {:ok, client} =
          Client.connect("localhost:#{port}", "remote-token",
            insecure: true,
            allow_insecure_remote_credentials: true
          )

        assert client.channel.headers == %{"authorization" => "Bearer remote-token"}
      end)
    end
  end

  describe "authority-shifting endpoint guard" do
    for endpoint <- @authority_shifting_endpoints do
      test "refuses #{inspect(endpoint)}" do
        assert_raise InsecureRemoteHostError, ~r/allow_insecure_remote_credentials/, fn ->
          Client.connect(unquote(endpoint), "super-secret-token", insecure: true)
        end
      end
    end
  end

  describe "TLS material validation" do
    for field <- [:ca_cert, :client_cert, :client_key] do
      test "refuses insecure combined with #{field}, rather than ignoring it" do
        opts = [{:insecure, true}, {unquote(field), "pem"}]

        assert_raise ArgumentError, ~r/insecure: true and TLS material/, fn ->
          Client.connect("localhost:50051", "token", opts)
        end
      end
    end

    test "still refuses a non-loopback insecure endpoint first, with material in hand" do
      assert_raise InsecureRemoteHostError, ~r/allow_insecure_remote_credentials/, fn ->
        Client.connect("evil.example.com:1234", "token", insecure: true, ca_cert: "pem")
      end
    end

    test "refuses a client certificate without its key" do
      assert_raise ArgumentError, ~r/client_key/, fn ->
        Client.connect("spicedb.example.com:443", "token", client_cert: "pem")
      end
    end

    test "refuses a client key without its certificate" do
      assert_raise ArgumentError, ~r/client_cert/, fn ->
        Client.connect("spicedb.example.com:443", "token", client_key: "pem")
      end
    end
  end

  defp with_tcp_listener(fun) do
    {:ok, listener} = :gen_tcp.listen(0, [:binary, active: false, ip: {127, 0, 0, 1}])
    {:ok, port} = :inet.port(listener)

    acceptor =
      spawn(fn ->
        case :gen_tcp.accept(listener, 5_000) do
          {:ok, _socket} -> :timer.sleep(2_000)
          _ -> :ok
        end
      end)

    try do
      fun.(port)
    after
      Process.exit(acceptor, :kill)
      :gen_tcp.close(listener)
    end
  end
end
