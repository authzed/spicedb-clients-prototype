# frozen_string_literal: true

module SpiceDB
  # The class-level constructors for {SpiceDB::Client} -- +new_plaintext+,
  # +new_system_tls+, and +new_custom_tls+ -- plus the block-form plumbing they
  # share.
  #
  # +extend+ed rather than +include+d, since these are class methods:
  # +SpiceDB::Client.new_plaintext(...)+ resolves here and reaches +Client.new+
  # through +self+, so every call site is unchanged by the extraction.
  #
  # Extracted the same way {SpiceDB::CaveatContext}, {SpiceDB::Retrying}, and
  # {SpiceDB::WatchMapping} were, and for the same reason: Client's
  # +Metrics/ClassLength+ ceiling exists to keep that class from growing without
  # bound, and the fix is to move a coherent concern out rather than to raise the
  # ceiling. Connection setup is that concern here.
  #
  # Constants still live on Client (+SpiceDB::Client::DEFAULT_TIMEOUT_SECONDS+ is
  # public API), so they are named as +Client::...+ below -- a module's constant
  # lookup is lexical and would not otherwise find them.
  module Connecting
    # Creates a client with an insecure (plaintext) connection.
    # Use this for testing only — the lack of TLS is made obvious by the name.
    #
    # If a block is given, the client is yielded and cleanup is ensured.
    #
    # By itself, this only works against a loopback +endpoint+ (localhost,
    # 127.0.0.0/8, ::1, or a unix socket target) -- the local-development case that
    # is the entire reason a plaintext connection exists. For any other endpoint,
    # pass +allow_insecure_remote_credentials: true+, on purpose, if you genuinely
    # mean to send a bearer token in cleartext to a remote host. See root
    # DESIGN.md, "RULE: Credentials over insecure transport require an explicit
    # opt-in".
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "localhost:50051")
    # @param token [String] the preshared key for authentication
    # @param default_timeout [Numeric] seconds applied to every unary call that
    #   does not pass its own `timeout:` (see {Client::DEFAULT_TIMEOUT_SECONDS}). NOT
    #   applied to streaming calls.
    # @param allow_insecure_remote_credentials [Boolean] explicit opt-in to send
    #   credentials in cleartext to a non-loopback endpoint.
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    # @raise [ArgumentError] if endpoint is not loopback and
    #   allow_insecure_remote_credentials is false.
    def new_plaintext(endpoint, token, default_timeout: Client::DEFAULT_TIMEOUT_SECONDS,
                      allow_insecure_remote_credentials: false, &)
      yield_or_return(
        new(endpoint: endpoint, token: token, insecure: true, default_timeout: default_timeout,
            allow_insecure_remote_credentials: allow_insecure_remote_credentials),
        &
      )
    end

    # Creates a client using the system's TLS certificate pool.
    # Use this for production connections.
    #
    # If a block is given, the client is yielded and cleanup is ensured.
    #
    # "System" here means whatever gRPC itself trusts by default, which is what
    # root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
    # clause 1 requires -- and that rule also names the catch: gRPC's C-core
    # compiles in its own +roots.pem+ rather than reading the host's trust store,
    # so a CA an operator installed on the machine is not honoured here. Use
    # {#new_custom_tls} for that case.
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "grpc.example.com:443")
    # @param token [String] the preshared key for authentication
    # @param default_timeout [Numeric] seconds applied to every unary call that
    #   does not pass its own `timeout:` (see {Client::DEFAULT_TIMEOUT_SECONDS}). NOT
    #   applied to streaming calls.
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    def new_system_tls(endpoint, token, default_timeout: Client::DEFAULT_TIMEOUT_SECONDS, &)
      yield_or_return(
        new(endpoint: endpoint, token: token, insecure: false, default_timeout: default_timeout),
        &
      )
    end

    # Creates a client using TLS trust material the caller supplies, rather than
    # gRPC's own roots.
    #
    # Use this to reach a SpiceDB fronted by a private or corporate CA, and to
    # present a client certificate to a server that requires mutual TLS. If a
    # block is given, the client is yielded and cleanup is ensured.
    #
    # Why this exists. Root DESIGN.md, "RULE: A system-TLS constructor must reach
    # a real server", permits {#new_system_tls} to delegate to the ecosystem's
    # default trust source precisely *because* a caller can supply their own
    # material when that default is not enough -- and it names the gap: gRPC's
    # C-core compiles in its own +roots.pem+, so a CA installed in the host's trust
    # store is invisible to {#new_system_tls}. This constructor is what closes it.
    #
    # +ca_cert+ REPLACES gRPC's built-in roots for this client rather than adding
    # to them; that is the C-core's behavior, and it is generally what a deployment
    # pinning a private CA wants.
    #
    # @param endpoint [String] the SpiceDB server address (e.g., "spicedb.internal:443")
    # @param token [String] the preshared key for authentication
    # @param ca_cert [String, nil] PEM root certificate(s) verifying SpiceDB's certificate
    # @param client_cert [String, nil] PEM certificate chain identifying this client
    # @param client_key [String, nil] PEM private key for +client_cert+
    # @param default_timeout [Numeric] seconds applied to every unary call that
    #   does not pass its own `timeout:` (see {Client::DEFAULT_TIMEOUT_SECONDS}). NOT
    #   applied to streaming calls.
    # @yield [client] optionally yields the client for block-scoped usage
    # @return [Client]
    # @raise [ArgumentError] if all three of +ca_cert+, +client_cert+ and
    #   +client_key+ are nil -- that is {#new_system_tls}, and a constructor named
    #   for custom trust material that silently used the default set instead would
    #   be a quiet way to believe a private CA was configured when it was not. Also
    #   if exactly one of +client_cert+/+client_key+ is supplied.
    def new_custom_tls(endpoint, token, ca_cert: nil, client_cert: nil, client_key: nil,
                       default_timeout: Client::DEFAULT_TIMEOUT_SECONDS, &)
      if ca_cert.nil? && client_cert.nil? && client_key.nil?
        raise ArgumentError,
              'spicedb: new_custom_tls needs at least one of ca_cert:, client_cert: or ' \
              'client_key: -- with none of them it would silently be new_system_tls, which ' \
              'uses the roots compiled into gRPC. Call new_system_tls directly if that is ' \
              'what you want'
      end

      yield_or_return(
        new(endpoint: endpoint, token: token, insecure: false, default_timeout: default_timeout,
            ca_cert: ca_cert, client_cert: client_cert, client_key: client_key),
        &
      )
    end

    private

    # Block form for every constructor above: yield the client and close it on the
    # way out (including on an exception), or hand it back for the caller to
    # manage. Private, so extending Client does not add it to the public surface.
    def yield_or_return(client)
      return client unless block_given?

      begin
        yield client
      ensure
        client.close
      end
    end
  end
end
