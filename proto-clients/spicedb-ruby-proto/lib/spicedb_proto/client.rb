# frozen_string_literal: true

require "grpc"
require "ipaddr"

module SpiceDBProto
  # Client wraps all generated gRPC service stubs for SpiceDB.
  #
  # @example Secure connection
  #   client = SpiceDBProto::Client.new("grpc.authzed.com:443", "my-token")
  #   client.permissions.check_permission(...)
  #   client.close
  #
  # @example Insecure connection (for testing)
  #   client = SpiceDBProto::Client.new("localhost:50051", "my-token", insecure: true)
  #
  class Client
    # Characters that can move which part of a target string a URI parser treats as the
    # authority: "@" (userinfo), "/" (path), "?" (query), "#" (fragment), and whitespace.
    # See .loopback_endpoint? below for why an endpoint holding any of them is refused
    # outright rather than parsed.
    AUTHORITY_SHIFTING = %r{[@/?\#]|\s}
    private_constant :AUTHORITY_SHIFTING

    # @return [Authzed::Api::V1::PermissionsService::Stub]
    attr_reader :permissions

    # @return [Authzed::Api::V1::SchemaService::Stub]
    attr_reader :schema

    # @return [Authzed::Api::V1::WatchService::Stub]
    attr_reader :watch

    # @return [Authzed::Api::V1::ExperimentalService::Stub]
    attr_reader :experimental

    # Creates a new SpiceDB proto client.
    #
    # @param endpoint [String] host:port of the SpiceDB server
    # @param token [String] bearer token for authentication
    # @param insecure [Boolean] if true, use an insecure (plaintext) channel
    # @param allow_insecure_remote_credentials [Boolean] by itself, +insecure+ only
    #   permits a plaintext connection to a loopback endpoint (localhost, 127.0.0.0/8,
    #   ::1, or a unix socket target) -- the local-development case that is the entire
    #   reason +insecure+ exists. Pass this as +true+, alongside +insecure+, only if you
    #   genuinely mean to send a bearer token in cleartext to a non-loopback host -- see
    #   root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
    #   opt-in". Named and separate from +insecure+ on purpose: the rule requires an
    #   option a reader cannot mistake for a default, not a boolean that does double duty
    #   as the plaintext-transport switch.
    # @param ca_cert [String, nil] PEM root certificate(s) used to verify SpiceDB's
    #   certificate, in place of the roots gRPC would otherwise use. Supply this to reach
    #   a SpiceDB fronted by a private or corporate CA.
    #
    #   Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
    #   requires the default secure path to delegate to the ecosystem's default trust
    #   source -- for grpc-ruby that is +GRPC::Core::ChannelCredentials.new+ with no
    #   arguments -- and names the hazard that leaves visible: gRPC's C-core compiles in
    #   its own +roots.pem+, so a CA an operator installed in the host's trust store is
    #   NOT honoured by the default. That rule permits delegating to the bundled set
    #   precisely *because* a caller can supply their own material instead; this is what
    #   makes that true. Passing +nil+ (the default) leaves the delegation exactly as it
    #   was -- the C-core treats a nil root argument as "use the built-in roots" -- so
    #   this library still selects no root set of its own, which clause 1 of that rule
    #   prohibits.
    # @param client_cert [String, nil] PEM certificate chain identifying this client, for
    #   a server that requires mutual TLS. Must be supplied together with +client_key+.
    # @param client_key [String, nil] PEM private key for +client_cert+. Must be supplied
    #   together with it.
    # @raise [ArgumentError] if +insecure+ is true, +endpoint+ is not loopback, and
    #   +allow_insecure_remote_credentials+ is false; if +insecure+ is true and any of
    #   +ca_cert+/+client_cert+/+client_key+ is supplied, since a plaintext channel
    #   performs no handshake to apply them to; or if exactly one of
    #   +client_cert+/+client_key+ is supplied -- raised before any channel,
    #   credential, or connection is created, so the token can never reach the wire for a
    #   rejected combination.
    def initialize(endpoint, token, insecure: false, allow_insecure_remote_credentials: false,
                   ca_cert: nil, client_cert: nil, client_key: nil)
      # See root DESIGN.md, "RULE: Credentials over insecure transport require an
      # explicit opt-in". This is the guard for the BearerTokenInterceptor bypass just
      # below (and the raw call-credentials proc a few lines further down) -- both exist
      # because channel credentials can't carry call credentials over a plaintext
      # channel, so nothing else here stops a bearer token from reaching an arbitrary
      # insecure host.
      if insecure && !allow_insecure_remote_credentials && !self.class.loopback_endpoint?(endpoint)
        raise ArgumentError,
          "spicedb: refusing to send credentials over an insecure (plaintext) connection to non-loopback endpoint #{endpoint.inspect}: " \
          "use TLS (pass insecure: false), or pass allow_insecure_remote_credentials: true if you intend to send a bearer token in cleartext to a remote host"
      end

      self.class.validate_tls_material!(
        insecure: insecure, ca_cert: ca_cert, client_cert: client_cert, client_key: client_key
      )

      # Build final credentials BEFORE constructing the channel, so exactly
      # one GRPC::Core::Channel is ever created. The previous implementation
      # built a channel with the bare (uncomposed) credentials first, then
      # -- for the secure path only -- built a SECOND channel with the
      # composed channel+call credentials and reassigned @channel to it,
      # leaking the first: #close only closes whichever channel @channel
      # currently references, so the discarded one was never closed.
      #
      # For secure channels, compose channel + call credentials up front.
      # For insecure channels, pass call credentials via the interceptor
      # instead (channel credentials can't carry call credentials over a
      # plaintext channel) -- endpoint has already been proven loopback (or
      # explicitly allowed) above.
      if insecure
        credentials = :this_channel_is_insecure
        @interceptor = BearerTokenInterceptor.new(token)
        stub_opts = { interceptors: [@interceptor] }
      else
        call_creds = GRPC::Core::CallCredentials.new(proc { |_context|
          { "authorization" => "Bearer #{token}" }
        })
        # Positional, and in the C-core's order: (pem_root_certs,
        # pem_private_key, pem_cert_chain). All nil is the same call as the
        # zero-arg form this used to make -- the C-core reads a nil root
        # argument as "use the built-in roots" -- so the default secure path
        # still delegates, per root DESIGN.md, "RULE: A system-TLS constructor
        # must reach a real server", clause 1. Note the key comes BEFORE the
        # certificate chain here, the reverse of how a caller names them.
        credentials = GRPC::Core::ChannelCredentials.new(ca_cert, client_key, client_cert)
                                                    .compose(call_creds)
        stub_opts = {}
      end

      @channel = GRPC::Core::Channel.new(endpoint, {}, credentials)
      stub_opts[:channel_override] = @channel

      @permissions = Authzed::Api::V1::PermissionsService::Stub.new(
        endpoint, nil, **stub_opts
      )
      @schema = Authzed::Api::V1::SchemaService::Stub.new(
        endpoint, nil, **stub_opts
      )
      @watch = Authzed::Api::V1::WatchService::Stub.new(
        endpoint, nil, **stub_opts
      )
      @experimental = Authzed::Api::V1::ExperimentalService::Stub.new(
        endpoint, nil, **stub_opts
      )
    end

    # Closes the underlying gRPC channel.
    def close
      @channel&.close
    end

    # Refuses a TLS configuration this client cannot honour. Called from the
    # constructor, before any channel or credential is created.
    #
    # Two refusals, both fail-closed:
    #
    # 1. *Trust material with +insecure+.* A plaintext channel performs no TLS
    #    handshake, so the material would simply be discarded and everything --
    #    including the bearer token, which this client hands to a
    #    BearerTokenInterceptor on that path -- would go out in cleartext, while the
    #    call site read as though TLS were configured. That is precisely the failure
    #    root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
    #    opt-in", exists to prevent, so supplying trust material must never become a
    #    second, quieter route to an insecure transport. Note it raises rather than
    #    "helpfully" turning TLS on: silently upgrading the transport would be just as
    #    much of a surprise in the other direction.
    # 2. *Half a client identity.* Neither +client_cert+ nor +client_key+ is usable
    #    alone. The C-core rejects the pair later, from a layer with no idea which
    #    argument the caller got wrong; failing here names it.
    #
    # @raise [ArgumentError] on either condition.
    def self.validate_tls_material!(insecure:, ca_cert:, client_cert:, client_key:)
      supplied = { ca_cert: ca_cert, client_cert: client_cert, client_key: client_key }
                 .reject { |_name, value| value.nil? }
                 .keys

      if insecure && !supplied.empty?
        raise ArgumentError,
          "spicedb: refusing to build a client with insecure: true and TLS material " \
          "(#{supplied.join(', ')}): a plaintext connection performs no TLS handshake, so the " \
          "material would be ignored and everything -- including the bearer token -- would be " \
          "sent in cleartext. Pass insecure: false to use TLS, or drop the TLS material to " \
          "connect in plaintext"
      end

      return if client_cert.nil? == client_key.nil?

      missing, present = client_key.nil? ? %i[client_key client_cert] : %i[client_cert client_key]
      raise ArgumentError,
        "spicedb: #{present} was supplied without #{missing}: mutual TLS needs both halves " \
        "of the client identity, and neither is usable alone"
    end

    # Reports whether the connection this client would open for +endpoint+ terminates
    # on a loopback destination: the literal hostname "localhost", an IP in
    # 127.0.0.0/8, the IPv6 loopback ::1, or a unix domain socket target (a "unix:"
    # prefix). A unix socket never leaves the host's kernel, so it is loopback for this
    # check even though it has no IP at all.
    #
    # That wording is deliberate. This does not answer "does this string look like it
    # names a loopback host"; it answers "will the transport dial loopback". Those are
    # the same question only if this method and the transport agree on where the host
    # ends and the rest of the target begins, and a hand-rolled split can always
    # diverge from the transport's own parse. The equivalent guard in this repo's C#,
    # Rust, TypeScript and Java clients diverged exactly that way: given
    # "127.0.0.1:443@evil.com" a last-colon split yields host "127.0.0.1" and reports
    # loopback, while their transports parsed the same string as a URI, read
    # "127.0.0.1:443" as userinfo, and connected to evil.com -- shipping the bearer
    # token there in cleartext.
    #
    # *grpc-ruby cannot reach its transport's parse.* The target is handed to grpc's
    # C-core, which parses it in C++ (grpc_core::URI::Parse plus SplitHostPort) and
    # exposes no Ruby-callable equivalent -- unlike Go, C#, Rust, TypeScript and Java,
    # where this guard now derives its host from the very parser the transport dials
    # with. So this method does the next best thing, in two parts:
    #
    # 1. Refuse outright any endpoint containing a character that could move the
    #    authority under URI parsing -- "@", "/", "?", "#", or whitespace. A legitimate
    #    SpiceDB target contains none of those, and failing closed on a weird endpoint
    #    is the correct trade for a credential leak. This is what actually closes the
    #    class here.
    # 2. Split what remains the way C-core's SplitHostPort does: a bracketed host must
    #    be followed by end-of-string or ":" + a numeric port, a string with two or more
    #    colons is a bare IPv6 literal, and only a single-colon "host:port" with a
    #    numeric port is split. Requiring a numeric port is what C-core does and is not
    #    decoration: dropping exactly that check from the C# guard is what opened the
    #    bypass above.
    #
    # (For the record, grpc-ruby was *not* exploitable by "127.0.0.1:443@evil.com":
    # C-core rejects it outright with "Failed to parse port in name" and never contacts
    # evil.com. The point of the above is to stop depending on that.)
    #
    # This is the exemption in root DESIGN.md, "RULE: Credentials over insecure
    # transport require an explicit opt-in": loopback is the reason insecure: true
    # exists at all (local development, docker-compose, CI), so it must keep working
    # with no extra ceremony. Anything else requires
    # allow_insecure_remote_credentials: true -- see #initialize above.
    #
    # @api private
    def self.loopback_endpoint?(endpoint)
      # Checked first, and only on the raw string: a unix target is not a URI authority
      # at all (it carries a filesystem path, so it legitimately contains the "/" the
      # reserved-character check below refuses), and it never leaves the host's kernel
      # regardless of what the path says.
      # Case-insensitive because a URI scheme is: C-core normalizes "UNIX:" and dials
      # the socket just the same, so a case-sensitive check here would refuse a target
      # the transport happily treats as local.
      return true if endpoint.match?(/\Aunix:/i)
      return false if endpoint.match?(AUTHORITY_SHIFTING)

      host =
        if (m = endpoint.match(/\A\[(.+)\]:\d+\z/))
          m[1] # "[::1]:50051" -> "::1"
        elsif (m = endpoint.match(/\A\[(.+)\]\z/))
          m[1] # "[::1]" -> "::1"
        elsif endpoint.start_with?("[")
          # A "[...]" prefix followed by anything else is not a form C-core would
          # accept as a bracketed host at all -- fail closed rather than guess.
          return false
        elsif endpoint.count(":") > 1
          endpoint # bare IPv6 (e.g. "::1") -- no port is possible without brackets
        elsif (idx = endpoint.rindex(":")) && endpoint[(idx + 1)..].match?(/\A\d+\z/)
          endpoint[0...idx] # "host:port", port numeric as C-core requires
        else
          endpoint # bare host, no port (or a colon C-core would not split on)
        end

      return true if host.casecmp("localhost").zero?

      begin
        IPAddr.new(host).loopback?
      rescue IPAddr::Error
        false
      end
    end
  end

  # Interceptor that injects a bearer token into request metadata.
  # Used for insecure channels where call credentials cannot be composed.
  #
  # @api private
  class BearerTokenInterceptor < GRPC::ClientInterceptor
    def initialize(token)
      super()
      @metadata = { "authorization" => "Bearer #{token}" }
    end

    def request_response(request:, call:, method:, metadata:, &block)
      metadata.merge!(@metadata)
      yield
    end

    def client_streamer(requests:, call:, method:, metadata:, &block)
      metadata.merge!(@metadata)
      yield
    end

    def server_streamer(request:, call:, method:, metadata:, &block)
      metadata.merge!(@metadata)
      yield
    end

    def bidi_streamer(requests:, call:, method:, metadata:, &block)
      metadata.merge!(@metadata)
      yield
    end
  end
end
