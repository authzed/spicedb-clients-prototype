defmodule SpiceDB.ConnectionTest do
  use ExUnit.Case, async: true

  test "plaintext to a non-loopback host is refused without the opt-in" do
    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_plaintext("spicedb.example.com:50051", "t")

    assert_raise SpiceDB.InvalidArgumentError, fn ->
      SpiceDB.new_plaintext!("10.0.0.1:50051", "t")
    end
  end

  test "custom TLS requires a CA with at least one certificate" do
    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", "t", [])

    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", "t", ca_cert: "not pem")
  end

  test "a client certificate without its key is refused" do
    {_key, cert} = test_cert()

    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", "t", ca_cert: cert, client_cert: cert)
  end

  test "well-paired but malformed client cert/key material is refused" do
    {_key, cert} = test_cert()

    # Well-paired (both present, so the presence/pairing check passes) but
    # garbled: a "CERTIFICATE REQUEST" block decodes to a shape client.ex's
    # decode_cert/1 does not recognize, so the proto tier returns
    # SpicedbProto.InvalidTlsMaterialError instead of letting a bare pattern
    # match raise. This client maps that to InvalidArgumentError the same way
    # it maps InsecureRemoteHostError.
    malformed_client_cert = """
    -----BEGIN CERTIFICATE REQUEST-----
    bm90IGEgcmVhbCBjZXJ0
    -----END CERTIFICATE REQUEST-----
    """

    malformed_client_key = """
    -----BEGIN RSA PRIVATE KEY-----
    Proc-Type: 4,ENCRYPTED
    DEK-Info: AES-128-CBC,0123456789ABCDEF0123456789ABCDEF

    bm90IGEgcmVhbCBrZXk=
    -----END RSA PRIVATE KEY-----
    """

    assert {:error, %SpiceDB.InvalidArgumentError{}} =
             SpiceDB.new_custom_tls("localhost:1", "t",
               ca_cert: cert,
               client_cert: malformed_client_cert,
               client_key: malformed_client_key
             )
  end

  test "unknown options are rejected" do
    assert_raise ArgumentError, fn -> SpiceDB.new_plaintext("localhost:1", "t", bogus: 1) end
  end

  defp test_cert do
    %{cert: der, key: key} = :public_key.pkix_test_root_cert(~c"test", [])
    {key, :public_key.pem_encode([{:Certificate, der, :not_encrypted}])}
  end
end
