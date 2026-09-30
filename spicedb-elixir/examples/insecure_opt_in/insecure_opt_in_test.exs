defmodule SpiceDB.Examples.InsecureOptInTest do
  use SpiceDB.ExampleCase, async: false

  test "allows loopback plaintext with no opt-in, and the client works" do
    {:ok, client} = SpiceDB.new_plaintext(endpoint(), token())
    on_exit(fn -> SpiceDB.close(client) end)

    assert SpiceDB.write_schema!(client, test_schema()) != ""
  end

  @tag :no_spicedb
  test "refuses a remote plaintext host without the opt-in" do
    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.new_plaintext("example.com:50051", token())

    assert message =~ "example.com:50051"
  end

  @tag :no_spicedb
  test "allows a remote plaintext host with the named opt-in, and the client works" do
    remote = "#{non_loopback_ip()}:#{port_of(endpoint())}"

    assert {:error, %SpiceDB.InvalidArgumentError{}} = SpiceDB.new_plaintext(remote, token())

    {:ok, client} =
      SpiceDB.new_plaintext(remote, token(), allow_insecure_remote_credentials: true)

    on_exit(fn -> SpiceDB.close(client) end)

    assert {:ok, {_schema, revision}} = SpiceDB.read_schema(client)
    assert revision != ""
  end

  for endpoint <- [
        "127.0.0.1:443@evil.com",
        "127.0.0.1:50051/../evil.com",
        "127.0.0.1:50051?x=evil.com",
        "127.0.0.1:50051#evil.com",
        "127.0.0.1 :50051"
      ] do
    @tag :no_spicedb
    @endpoint endpoint
    test "refuses #{endpoint}, whose authority could move under URI parsing" do
      assert {:error, %SpiceDB.InvalidArgumentError{}} = SpiceDB.new_plaintext(@endpoint, token())
    end
  end

  defp non_loopback_ip do
    {:ok, interfaces} = :inet.getifaddrs()

    ips =
      for {_name, opts} <- interfaces,
          {:addr, {a, _, _, _} = ip} <- opts,
          a != 127,
          do: ip

    case ips do
      [ip | _] -> ip |> :inet.ntoa() |> to_string()
      [] -> flunk("this host has no non-loopback IPv4 address to reach SpiceDB through")
    end
  end

  defp port_of(endpoint), do: endpoint |> String.split(":") |> List.last()
end
