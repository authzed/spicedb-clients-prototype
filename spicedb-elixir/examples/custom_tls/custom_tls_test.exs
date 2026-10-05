defmodule SpiceDB.Examples.CustomTLSTest do
  use SpiceDB.ExampleCase, async: false

  alias Authzed.Api.V1
  alias SpiceDB.Examples.StandIn

  @moduletag :no_spicedb

  defmodule TLSService do
    use GRPC.Server, service: Authzed.Api.V1.PermissionsService.Service

    def check_permission(_request, _stream) do
      %V1.CheckPermissionResponse{
        permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
        checked_at: %V1.ZedToken{token: "tls"}
      }
    end
  end

  setup_all do
    {:ok, pki: pki(), other: pki()}
  end

  defp pki do
    san =
      {:Extension, {2, 5, 29, 17}, false, [dNSName: ~c"localhost", iPAddress: <<127, 0, 0, 1>>]}

    chain = %{
      root: [key: {:rsa, 2048, 65_537}],
      peer: [key: {:rsa, 2048, 65_537}, extensions: [san]]
    }

    %{server_config: server, client_config: client} =
      :public_key.pkix_test_data(%{server_chain: chain, client_chain: chain})

    %{
      server: server,
      ca_pem: pem(Enum.map(Keyword.fetch!(client, :cacerts), &{:Certificate, &1})),
      client_cert_pem: pem([{:Certificate, Keyword.fetch!(client, :cert)}]),
      client_key_pem: pem([Keyword.fetch!(client, :key)])
    }
  end

  defp pem(entries),
    do:
      :public_key.pem_encode(Enum.map(entries, fn {type, der} -> {type, der, :not_encrypted} end))

  defp start_tls_server(pki, opts \\ []) do
    server = pki.server

    ssl =
      [cert: server[:cert], key: server[:key], cacerts: server[:cacerts]] ++
        if(opts[:mtls], do: [verify: :verify_peer, fail_if_no_peer_cert: true], else: [])

    StandIn.start!([TLSService], cred: GRPC.Credential.new(ssl: ssl))
  end

  defp rel, do: Relationship.from_tuple!("document:readme#viewer@user:alice")

  defp check(client),
    do: SpiceDB.check_permission(client, Consistency.full(), "view", rel(), timeout: 10_000)

  defp connect(port, opts) do
    with {:ok, client} <- SpiceDB.new_custom_tls("localhost:#{port}", token(), opts) do
      on_exit(fn -> SpiceDB.close(client) end)
      {:ok, client}
    end
  end

  test "reaches a SpiceDB behind a private CA", %{pki: pki} do
    port = start_tls_server(pki)

    {:ok, client} = connect(port, ca_cert: pki.ca_pem)
    assert {:ok, result} = check(client)
    assert CheckResult.has_permission?(result)
    assert result.checked_at == "tls"
  end

  test "refuses a server whose certificate the given CA did not sign", %{pki: pki, other: other} do
    port = start_tls_server(pki)

    result =
      case connect(port, ca_cert: other.ca_pem) do
        {:ok, client} -> check(client)
        error -> error
      end

    assert {:error, %SpiceDB.UnavailableError{message: message}} = result
    assert message =~ "unknown_ca"
  end

  test "presents a client certificate where the server requires mutual TLS", %{pki: pki} do
    port = start_tls_server(pki, mtls: true)

    {:ok, client} =
      connect(port,
        ca_cert: pki.ca_pem,
        client_cert: pki.client_cert_pem,
        client_key: pki.client_key_pem
      )

    assert {:ok, result} = check(client)
    assert CheckResult.has_permission?(result)
  end

  test "is refused by a mutual-TLS server when no client certificate is presented", %{pki: pki} do
    port = start_tls_server(pki, mtls: true)

    result =
      case connect(port, ca_cert: pki.ca_pem) do
        {:ok, client} -> check(client)
        error -> error
      end

    assert {:error, %SpiceDB.UnavailableError{message: message}} = result
    assert message =~ "certificate_required"
  end

  test "rejects trust material that holds no certificate" do
    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", token(), ca_cert: "not a certificate")

    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", token(), [])
  end

  test "rejects half a client identity", %{pki: pki} do
    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.new_custom_tls("localhost:1", token(),
               ca_cert: pki.ca_pem,
               client_cert: pki.client_cert_pem
             )

    assert message =~ "client_key"
  end
end
