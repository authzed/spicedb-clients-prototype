defmodule SpicedbProto.ClientTest do
  use ExUnit.Case, async: true

  alias SpicedbProto.Client
  alias SpicedbProto.InsecureRemoteHostError
  alias SpicedbProto.InvalidTlsMaterialError

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
      assert {:error, %InsecureRemoteHostError{message: message}} =
               Client.connect("evil.example.com:1234", "super-secret-token", insecure: true)

      assert message =~ ~r/evil\.example\.com:1234/
    end

    test "names the opt-in in the error message" do
      assert {:error, %InsecureRemoteHostError{message: message}} =
               Client.connect("evil.example.com:1234", "super-secret-token", insecure: true)

      assert message =~ ~r/allow_insecure_remote_credentials/
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
        assert {:error, %InsecureRemoteHostError{message: message}} =
                 Client.connect(unquote(endpoint), "super-secret-token", insecure: true)

        assert message =~ ~r/allow_insecure_remote_credentials/
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
      assert {:error, %InsecureRemoteHostError{message: message}} =
               Client.connect("evil.example.com:1234", "token", insecure: true, ca_cert: "pem")

      assert message =~ ~r/allow_insecure_remote_credentials/
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

  describe "malformed TLS material" do
    # Well-paired (both present, so validate_tls_material!/4's presence/pairing
    # check passes) but garbled: :public_key.pem_decode/1 never raises -- it
    # happily returns a plain list for both of these -- but neither entry has
    # the shape client.ex's decode_cert/1 and decode_key/1 expect. A
    # "CERTIFICATE REQUEST" PEM block decodes to {:CertificationRequest, _,
    # :not_encrypted} (wrong atom tag, so decode_cert/1's bare match used to
    # raise MatchError), and an encrypted "RSA PRIVATE KEY" block decodes to
    # {:RSAPrivateKey, _, {cipher, iv}} (the third element is a 2-tuple, not
    # the atom :not_encrypted, so decode_key/1's one-clause case used to raise
    # CaseClauseError). Verified empirically against this exact PEM content
    # before writing this test: a base64-garbled but otherwise well-formed
    # CERTIFICATE or PRIVATE KEY block (matching type, :not_encrypted) decodes
    # into the expected shape and does NOT exercise the crash -- pem_decode
    # does not validate DER content, only the block's own header -- so this
    # test deliberately uses the wrong block type/encryption header instead.
    @malformed_client_cert """
    -----BEGIN CERTIFICATE REQUEST-----
    bm90IGEgcmVhbCBjZXJ0
    -----END CERTIFICATE REQUEST-----
    """

    @malformed_client_key """
    -----BEGIN RSA PRIVATE KEY-----
    Proc-Type: 4,ENCRYPTED
    DEK-Info: AES-128-CBC,0123456789ABCDEF0123456789ABCDEF

    bm90IGEgcmVhbCBrZXk=
    -----END RSA PRIVATE KEY-----
    """

    test "returns an error instead of crashing on a malformed client_cert" do
      assert {:error, %InvalidTlsMaterialError{message: message}} =
               Client.connect("spicedb.example.com:443", "token",
                 client_cert: @malformed_client_cert,
                 client_key: @malformed_client_key
               )

      assert message =~ ~r/client_cert/
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
