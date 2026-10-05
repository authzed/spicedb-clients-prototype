defmodule SpicedbProto.InsecureRemoteHostError do
  @moduledoc """
  Returned as `{:error, %SpicedbProto.InsecureRemoteHostError{}}` by
  `SpicedbProto.Client.connect/3` when a plaintext connection to a
  non-loopback endpoint is requested without
  `allow_insecure_remote_credentials: true`.

  A distinct exception struct rather than a bare term so the idiomatic
  client can match this refusal without also catching the TLS trust-material
  validation errors `SpicedbProto.Client.connect/3` raises for unrelated
  reasons. See root DESIGN.md, "RULE: Credentials over insecure transport
  require an explicit opt-in", clause 4.
  """
  defexception [:message]
end

defmodule SpicedbProto.InvalidTlsMaterialError do
  @moduledoc """
  Returned as `{:error, %SpicedbProto.InvalidTlsMaterialError{}}` by
  `SpicedbProto.Client.connect/3` when the PEM content supplied for
  `ca_cert`, `client_cert`, or `client_key` is present and well-paired (has
  already passed `validate_tls_material!/4`) but cannot be decoded into the
  certificate or private key shape this client expects.

  `:public_key.pem_decode/1` itself never raises -- it returns a plain list,
  and decoding one of its entries into a usable certificate or key is this
  client's own job. This error is what that job returns instead of raising
  when an entry does not have the expected shape: an encrypted private key,
  a PEM block of the wrong type (a certificate request where a certificate
  was expected, say), or a PEM string holding zero or more than one block
  where exactly one is expected.
  """
  defexception [:message]
end

defmodule SpicedbProto.Client do
  @moduledoc """
  Wraps a gRPC channel to a SpiceDB server.

  Unlike this repo's Ruby/Go/Java clients, a generated grpc-elixir service
  module takes the channel as an explicit argument on every call (for example
  `Authzed.Api.V1.PermissionsService.Stub.check_permission(client.channel,
  request)`) rather than binding to it at stub-construction time. So this
  module holds only the channel -- there is nothing analogous to Ruby's
  `Client#permissions`/`#schema`/etc. readers to build.

  ## Examples

      {:ok, client} = SpicedbProto.Client.connect("grpc.authzed.com:443", "my-token")
      {:ok, client} = SpicedbProto.Client.connect("localhost:50051", "my-token", insecure: true)
  """

  alias GRPC.Credential

  defstruct [:channel]

  @type t :: %__MODULE__{channel: GRPC.Channel.t()}

  @default_adapter GRPC.Client.Adapters.Mint

  # Characters that can move which part of a target string a URI parser treats as the
  # authority: "@" (userinfo), "/" (path), "?" (query), "#" (fragment), and whitespace.
  # See loopback_endpoint?/1 below for why an endpoint holding any of them is refused
  # outright rather than parsed.
  @authority_shifting ~r/[@\/?#]|\s/

  @doc """
  Connects to a SpiceDB server and returns a client wrapping the resulting
  gRPC channel.

  ## Parameters

    * `endpoint` - host:port of the SpiceDB server (bracketed for IPv6, e.g.
      "[::1]:50051"), or a "unix:" socket path.
    * `token` - bearer token sent as `authorization: Bearer <token>` metadata
      on every call, via the channel-level `:headers` option that grpc-elixir
      merges into every request -- see `GRPC.Transport.HTTP2.client_headers_without_reserved/2`
      in grpc_core. No per-call interceptor is needed.
    * `opts`:
      * `:insecure` - if `true`, use a plaintext channel. Defaults to `false`.
      * `:allow_insecure_remote_credentials` - by itself, `insecure: true` only
        permits a plaintext connection to a loopback endpoint (localhost,
        127.0.0.0/8, ::1, or a unix socket target) -- the local-development
        case that is the entire reason `insecure` exists. Pass this as `true`,
        alongside `insecure: true`, only if you genuinely mean to send a
        bearer token in cleartext to a non-loopback host -- see root
        DESIGN.md, "RULE: Credentials over insecure transport require an
        explicit opt-in". Named and separate from `insecure` on purpose: the
        rule requires an option a reader cannot mistake for a default, not a
        boolean that does double duty as the plaintext-transport switch.
      * `:ca_cert` - PEM root certificate(s) used to verify SpiceDB's
        certificate, in place of the roots this client would otherwise use.
        Supply this to reach a SpiceDB fronted by a private or corporate CA.

        Root DESIGN.md, "RULE: A system-TLS constructor must reach a real
        server", requires the default secure path to delegate to the
        ecosystem's default trust source. grpc-elixir's own zero-config
        default (triggered when no `:cred` is passed) falls back to the
        `castore` package -- a statically bundled Mozilla CA list, not the OS
        trust store -- which is not operator-writable. Rather than depend on
        that fallback, this client builds its own default credential from
        `:public_key.cacerts_get/0` (the OS trust store, operator-writable on
        every platform that ships one), so the default secure path here is
        nicer than grpc-elixir's own: overriding it is optional, not
        required, per that RULE's distinction between operator-writable and
        non-operator-writable defaults.
      * `:client_cert` - PEM certificate chain identifying this client, for a
        server that requires mutual TLS. Must be supplied together with
        `:client_key`.
      * `:client_key` - PEM private key for `:client_cert`. Must be supplied
        together with it.

  Returns `{:ok, client}` or `{:error, reason}`. Returns
  `{:error, %SpicedbProto.InsecureRemoteHostError{}}` if `insecure: true`,
  `endpoint` is not loopback, and `allow_insecure_remote_credentials` is
  false -- checked before any channel or credential is built, so the token
  can never reach the wire for a rejected combination. Also returns
  `{:error, %SpicedbProto.InvalidTlsMaterialError{}}` if `ca_cert`,
  `client_cert`, or `client_key` is present but its PEM content cannot be
  decoded into the certificate or private key shape expected (an encrypted
  private key, or a PEM block of the wrong type, for example) -- checked
  after the presence/pairing validation below passes, and before any channel
  is built. Like `InsecureRemoteHostError`, this is *returned*, not raised.
  Raises `ArgumentError` if `insecure: true` and any of
  `:ca_cert`/`:client_cert`/`:client_key` is supplied, since a plaintext
  channel performs no handshake to apply them to; or if exactly one of
  `:client_cert`/`:client_key` is supplied. That raise also happens before
  any channel or credential is built.
  """
  @spec connect(String.t(), String.t(), keyword()) :: {:ok, t()} | {:error, term()}
  def connect(endpoint, token, opts \\ []) do
    insecure = Keyword.get(opts, :insecure, false)

    allow_insecure_remote_credentials =
      Keyword.get(opts, :allow_insecure_remote_credentials, false)

    ca_cert = Keyword.get(opts, :ca_cert)
    client_cert = Keyword.get(opts, :client_cert)
    client_key = Keyword.get(opts, :client_key)

    if insecure && !allow_insecure_remote_credentials && !loopback_endpoint?(endpoint) do
      {:error,
       %SpicedbProto.InsecureRemoteHostError{
         message:
           "spicedb: refusing to send credentials over an insecure (plaintext) connection to non-loopback endpoint #{inspect(endpoint)}: " <>
             "use TLS (pass insecure: false), or pass allow_insecure_remote_credentials: true if you intend to send a bearer token in cleartext to a remote host"
       }}
    else
      validate_tls_material!(insecure, ca_cert, client_cert, client_key)

      connect_opts = [
        adapter: @default_adapter,
        headers: %{"authorization" => "Bearer #{token}"}
      ]

      if insecure do
        do_connect(endpoint, connect_opts)
      else
        case build_credential(ca_cert, client_cert, client_key) do
          {:ok, credential} ->
            do_connect(endpoint, Keyword.put(connect_opts, :cred, credential))

          {:error, %SpicedbProto.InvalidTlsMaterialError{}} = error ->
            error
        end
      end
    end
  end

  defp do_connect(endpoint, connect_opts) do
    case GRPC.Stub.connect(normalize_endpoint(endpoint), connect_opts) do
      {:ok, channel} -> {:ok, %__MODULE__{channel: channel}}
      {:error, reason} -> {:error, reason}
    end
  end

  @doc "Closes the underlying gRPC channel."
  @spec close(t()) :: :ok
  def close(%__MODULE__{channel: channel}),
    do: GRPC.Stub.disconnect(channel) |> then(fn _ -> :ok end)

  # grpc-elixir 1.0.5's schemeless target handling (GRPC.Client.Connection's
  # private normalize_target_and_opts/2) splits an unscoped target on every
  # colon and only matches a 2-element result -- so a bracketed IPv6 literal
  # with no scheme prefix, e.g. "[::1]:50051", raises a CaseClauseError
  # instead of connecting (verified empirically against grpc 1.0.5). This
  # client's public endpoint format matches every other language client here
  # (bracketed IPv6 allowed, no scheme prefix required), so it adds the
  # "ipv6:" prefix grpc-elixir needs whenever the endpoint is bracketed.
  @spec normalize_endpoint(String.t()) :: String.t()
  defp normalize_endpoint(endpoint) do
    if String.starts_with?(endpoint, "["), do: "ipv6:" <> endpoint, else: endpoint
  end

  defp build_credential(nil, nil, nil) do
    {:ok, Credential.new(ssl: default_ssl_opts())}
  end

  defp build_credential(ca_cert, client_cert, client_key) do
    with {:ok, ssl_opts} <- put_cacerts(default_ssl_opts(), ca_cert),
         {:ok, ssl_opts} <- put_client_cert(ssl_opts, client_cert),
         {:ok, ssl_opts} <- put_client_key(ssl_opts, client_key) do
      {:ok, Credential.new(ssl: ssl_opts)}
    end
  end

  defp put_cacerts(ssl_opts, nil), do: {:ok, ssl_opts}

  defp put_cacerts(ssl_opts, ca_cert) do
    with {:ok, ders} <- decode_certs(:ca_cert, ca_cert) do
      {:ok, Keyword.put(ssl_opts, :cacerts, ders)}
    end
  end

  defp put_client_cert(ssl_opts, nil), do: {:ok, ssl_opts}

  defp put_client_cert(ssl_opts, client_cert) do
    with {:ok, der} <- decode_cert(:client_cert, client_cert) do
      {:ok, Keyword.put(ssl_opts, :cert, der)}
    end
  end

  defp put_client_key(ssl_opts, nil), do: {:ok, ssl_opts}

  defp put_client_key(ssl_opts, client_key) do
    with {:ok, key} <- decode_key(:client_key, client_key) do
      {:ok, Keyword.put(ssl_opts, :key, key)}
    end
  end

  defp default_ssl_opts do
    [cacerts: :public_key.cacerts_get(), verify: :verify_peer, depth: 99]
  end

  defp decode_certs(option, pem) do
    pem
    |> :public_key.pem_decode()
    |> Enum.reduce_while({:ok, []}, fn
      {:Certificate, der, :not_encrypted}, {:ok, ders} -> {:cont, {:ok, [der | ders]}}
      other, _acc -> {:halt, {:error, invalid_tls_material_error(option, "certificate", other)}}
    end)
    |> case do
      {:ok, ders} -> {:ok, Enum.reverse(ders)}
      {:error, _} = error -> error
    end
  end

  defp decode_cert(option, pem) do
    case :public_key.pem_decode(pem) do
      [{:Certificate, der, :not_encrypted}] ->
        {:ok, der}

      other ->
        {:error, invalid_tls_material_error(option, "certificate", other)}
    end
  end

  defp decode_key(option, pem) do
    case :public_key.pem_decode(pem) do
      [{type, der, :not_encrypted}] ->
        {:ok, {type, der}}

      other ->
        {:error, invalid_tls_material_error(option, "private key", other)}
    end
  end

  defp invalid_tls_material_error(option, kind, decoded) do
    %SpicedbProto.InvalidTlsMaterialError{
      message:
        "spicedb: #{option} could not be decoded as a #{kind}: :public_key.pem_decode/1 returned " <>
          "#{inspect(decoded)}, expected exactly one PEM block with no encryption"
    }
  end

  # Refuses a TLS configuration this client cannot honour. Called before any
  # channel or credential is created.
  #
  # Two refusals, both fail-closed:
  #
  # 1. Trust material with insecure: true. A plaintext channel performs no TLS
  #    handshake, so the material would simply be discarded and everything --
  #    including the bearer token -- would go out in cleartext, while the
  #    call site read as though TLS were configured. That is precisely the
  #    failure root DESIGN.md, "RULE: Credentials over insecure transport
  #    require an explicit opt-in", exists to prevent.
  # 2. Half a client identity. Neither client_cert nor client_key is usable
  #    alone; failing here names the problem clearly instead of deferring to
  #    a lower layer that has no idea which argument the caller got wrong.
  defp validate_tls_material!(insecure, ca_cert, client_cert, client_key) do
    supplied =
      [ca_cert: ca_cert, client_cert: client_cert, client_key: client_key]
      |> Enum.reject(fn {_name, value} -> is_nil(value) end)
      |> Enum.map(&elem(&1, 0))

    if insecure && supplied != [] do
      raise ArgumentError,
            "spicedb: refusing to build a client with insecure: true and TLS material (#{Enum.join(supplied, ", ")}): " <>
              "a plaintext connection performs no TLS handshake, so the material would be ignored and everything -- " <>
              "including the bearer token -- would be sent in cleartext. Pass insecure: false to use TLS, or drop the TLS material to connect in plaintext"
    end

    if is_nil(client_cert) != is_nil(client_key) do
      {present, missing} =
        if is_nil(client_key), do: {:client_cert, :client_key}, else: {:client_key, :client_cert}

      raise ArgumentError,
            "spicedb: #{present} was supplied without #{missing}: mutual TLS needs both halves of the client identity, and neither is usable alone"
    end

    :ok
  end

  @doc """
  Reports whether the connection this client would open for `endpoint`
  terminates on a loopback destination: the literal hostname "localhost", an
  IP in 127.0.0.0/8, the IPv6 loopback ::1, or a unix domain socket target (a
  "unix:" prefix).

  That wording is deliberate: this does not answer "does this string look
  like it names a loopback host", it answers "will the transport dial
  loopback". Those are the same question only if this function and the
  transport agree on where the host ends and the rest of the target begins,
  and a hand-rolled split can always diverge from the transport's own parse.

  grpc-elixir's real resolution path is not reachable from here: the fallback
  it uses for arbitrary "host:port" targets, `GRPC.Client.Connection.EndpointResolver`,
  is marked `@moduledoc false` (undocumented, not part of its public API), so
  depending on it directly would tie this guard to an implementation detail
  a patch release is free to change. This is the same tradeoff this repo's
  Ruby client carries permanently for the equivalent reason (grpc-ruby parses
  targets in C-core, with no Elixir/Ruby-callable equivalent). So this
  function does the next best thing, in two parts:

  1. Refuse outright any endpoint containing a character that could move the
     authority under URI parsing -- "@", "/", "?", "#", or whitespace. A
     legitimate SpiceDB target contains none of those, and failing closed on
     a weird endpoint is the correct trade for a credential leak.
  2. Split what remains: a bracketed host must be followed by end-of-string
     or ":" plus a numeric port, a string with two or more colons and no
     brackets is a bare IPv6 literal, and only a single-colon "host:port"
     with a numeric port is split.

  (For the record, grpc-elixir 1.0.5 was verified *not* exploitable by
  "127.0.0.1:443@evil.com": it raises `ArgumentError` from
  `:erlang.binary_to_integer/1` while parsing the port and never contacts
  evil.com. The point of the above is to stop depending on that.)
  """
  @spec loopback_endpoint?(String.t()) :: boolean()
  def loopback_endpoint?(endpoint) do
    cond do
      Regex.match?(~r/\Aunix:/i, endpoint) ->
        true

      Regex.match?(@authority_shifting, endpoint) ->
        false

      true ->
        endpoint |> extract_host() |> loopback_host?()
    end
  end

  defp extract_host(endpoint) do
    cond do
      m = Regex.run(~r/\A\[(.+)\]:\d+\z/, endpoint) ->
        Enum.at(m, 1)

      m = Regex.run(~r/\A\[(.+)\]\z/, endpoint) ->
        Enum.at(m, 1)

      String.starts_with?(endpoint, "[") ->
        :invalid

      length(String.split(endpoint, ":")) > 2 ->
        endpoint

      true ->
        case String.split(endpoint, ":") do
          [host, port] ->
            if Regex.match?(~r/\A\d+\z/, port), do: host, else: endpoint

          _ ->
            endpoint
        end
    end
  end

  defp loopback_host?(:invalid), do: false

  defp loopback_host?(host) do
    if String.downcase(host) == "localhost" do
      true
    else
      case :inet.parse_address(String.to_charlist(host)) do
        {:ok, ip} -> loopback_ip?(ip)
        {:error, _} -> false
      end
    end
  end

  defp loopback_ip?({127, _, _, _}), do: true
  defp loopback_ip?({0, 0, 0, 0, 0, 0, 0, 1}), do: true
  defp loopback_ip?(_), do: false
end
