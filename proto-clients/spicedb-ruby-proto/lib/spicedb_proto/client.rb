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
  # Raised when a plaintext connection to a non-loopback endpoint is requested
  # without +allow_insecure_remote_credentials+.
  #
  # A distinct class rather than a bare ArgumentError so the idiomatic client can
  # map this refusal onto its own error without rescuing every ArgumentError the
  # constructor might raise -- the TLS trust-material validations beside it are
  # different argument errors and must not be reclassified with it. It still
  # subclasses ArgumentError, so existing rescues keep working. See root
  # DESIGN.md, "RULE: Credentials over insecure transport require an explicit
  # opt-in", clause 4.
  class InsecureRemoteHostError < ArgumentError; end

  # Raised at construction for an endpoint this client cannot dial: an
  # unsupported scheme, a URL carrying a path, query, fragment or userinfo, a
  # non-numeric port, an +http://+ URL without a port, or a URL scheme that
  # disagrees with the transport. Subclasses ArgumentError like
  # InsecureRemoteHostError, so existing rescues keep working.
  class InvalidEndpointError < ArgumentError; end

  class Client
    # Characters that can move which part of a target string a URI parser treats as the
    # authority: "@" (userinfo), "/" (path), "?" (query), "#" (fragment), and whitespace.
    # See .loopback_endpoint? below for why an endpoint holding any of them is refused
    # outright rather than parsed.
    AUTHORITY_SHIFTING = %r{[@/?\#]|\s}
    private_constant :AUTHORITY_SHIFTING

    # The forms .parse_endpoint accepts, named in every endpoint error.
    ENDPOINT_FORMS = "expected host:port, http://host:port (plaintext), https://host[:port] (TLS), " \
                     "dns:///host:port or a unix: socket path"

    # Each character that cannot appear in a dialable authority, with the
    # problem it signals, in the order they are checked.
    MALFORMED_AUTHORITY = [
      ["@", "userinfo is not supported"],
      ["/", "a path is not supported"],
      ["?", "a query is not supported"],
      ["#", "a fragment is not supported"]
    ].freeze
    private_constant :MALFORMED_AUTHORITY

    # @return [Authzed::Api::V1::PermissionsService::Stub]
    attr_reader :permissions

    # @return [Authzed::Api::V1::SchemaService::Stub]
    attr_reader :schema

    # @return [Authzed::Api::V1::WatchService::Stub]
    attr_reader :watch

    # @return [Authzed::Api::V1::ExperimentalService::Stub]
    attr_reader :experimental

    # @return [Authzed::Api::Materialize::V0::RoaringLookupResourcesService::Stub]
    #   Generated from authzed.api.materialize.v0, a separate proto package from
    #   the other four stubs above (authzed.api.v1) -- see ExperimentalRoaringLookupResources
    #   in that package's proto source. Named for the service it wraps rather than
    #   grouped under +experimental+, since it is a distinct generated package with its
    #   own stub class, not another RPC on Authzed::Api::V1::ExperimentalService::Stub.
    attr_reader :materialize, :materialize_relationships, :permission_sets

    # @return [GRPC::Core::Channel] the channel every stub shares, for readiness
    #   checks (+connectivity_state+, +watch_connectivity_state+)
    attr_reader :channel

    # Creates a new SpiceDB proto client.
    #
    # @param endpoint [String] the SpiceDB server: +host:port+, +http://host:port+
    #   (plaintext; +insecure+ must be true) or +https://host[:port]+ (TLS, port
    #   443 by default; +insecure+ must be false). See .parse_endpoint.
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
      endpoint = self.class.validate_connection!(
        endpoint, insecure: insecure, allow_insecure_remote_credentials: allow_insecure_remote_credentials,
        ca_cert: ca_cert, client_cert: client_cert, client_key: client_key
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
      @owns_channel = true
      build_stubs(endpoint, stub_opts)
    end

    # Runs every refusal #initialize applies, in the same order, without
    # creating any gRPC object (no channel, no credentials), so a caller that
    # connects later -- the idiomatic client opens its channel on first use, so
    # a client built before a fork works in the forked children -- can still
    # fail at construction.
    #
    # @return [String] the gRPC target to dial (see .parse_endpoint)
    # @raise [InvalidEndpointError] see .parse_endpoint; also a URL scheme
    #   that disagrees with +insecure+. Checked first, so the remote-plaintext
    #   refusal below only ever names a well-formed host.
    # @raise [InsecureRemoteHostError] plaintext to a non-loopback endpoint
    #   without +allow_insecure_remote_credentials+
    # @raise [ArgumentError] see .validate_tls_material!
    def self.validate_connection!(endpoint, insecure: false, allow_insecure_remote_credentials: false,
                                  ca_cert: nil, client_cert: nil, client_key: nil)
      original = endpoint
      endpoint, scheme = parse_endpoint(endpoint, insecure: insecure)
      # A dns:/// target's loopback check runs on the host:port it resolves.
      checked = scheme == :dns ? endpoint.delete_prefix("dns:///") : endpoint

      # See root DESIGN.md, "RULE: Credentials over insecure transport require an
      # explicit opt-in". This is the guard for the BearerTokenInterceptor bypass in
      # #initialize (and the raw call-credentials proc beside it) -- both exist
      # because channel credentials can't carry call credentials over a plaintext
      # channel, so nothing else stops a bearer token from reaching an arbitrary
      # insecure host.
      if insecure && !allow_insecure_remote_credentials && !loopback_endpoint?(checked)
        raise InsecureRemoteHostError,
          "spicedb: refusing to send credentials over an insecure (plaintext) connection to non-loopback endpoint #{original.inspect}: " \
          "to allow it, pass both insecure: true and allow_insecure_remote_credentials: true; otherwise use TLS (insecure: false)"
      end

      validate_tls_material!(insecure: insecure, ca_cert: ca_cert, client_cert: client_cert, client_key: client_key)
      endpoint
    end

    # Parses an endpoint into the gRPC target this client dials and the URL
    # scheme it was written with (+:http+, +:https+, +:dns+, or nil for a bare
    # host or a +unix:+ target).
    #
    # Accepted forms (root DESIGN.md, "Accept the endpoint forms deployments
    # use, or reject them at construction"):
    #
    # - +host:port+ (and a +unix:+ socket target, passed to gRPC unchanged);
    # - +http://host:port+, which selects plaintext;
    # - +https://host[:port]+, which selects TLS;
    # - +dns:///host[:port]+, gRPC's DNS resolver target (client-side load
    #   balancing across every address the name resolves to), dialed as
    #   +dns:///host:port+ with the same port rule. An explicit DNS server
    #   authority (+dns://8.8.8.8/host:port+) and the slashless +dns:host:port+
    #   are refused.
    #
    # The port rule is the same with or without a scheme: plaintext needs an
    # explicit port, so no client silently dials port 80 or 443, and TLS
    # defaults to 443.
    #
    # grpc-ruby's C-core does not understand +http://+ or +https://+ targets (it
    # fails every call with "Failed to parse port in name"), so every endpoint
    # is reduced here to the +host:port+ the transport dials. One trailing "/"
    # after a URL's authority is allowed. Any other scheme or gRPC target form,
    # a scheme that disagrees with +insecure+, and userinfo, a path, a query, a
    # fragment or a non-numeric port raise naming that problem.
    #
    # @param endpoint [String]
    # @param insecure [Boolean] whether the client uses plaintext
    # @return [Array(String, Symbol)] the target to dial and the scheme (or nil)
    # @raise [InvalidEndpointError]
    def self.parse_endpoint(endpoint, insecure: false)
      raise InvalidEndpointError, "spicedb: endpoint must be a String, got #{endpoint.class}" unless endpoint.is_a?(String)
      return [endpoint, nil] if endpoint.match?(/\Aunix(-abstract)?:/i)
      return parse_dns_target(endpoint, insecure) if endpoint.match?(/\Adns:/i)

      scheme = nil
      authority = endpoint
      if (m = endpoint.match(%r{\A([A-Za-z][A-Za-z0-9+.-]*)://(.*)\z}m))
        scheme = m[1].downcase.to_sym
        unless %i[http https].include?(scheme)
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "unsupported scheme #{m[1].inspect}")
        end
        if scheme == :http && !insecure
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "http:// selects plaintext, but this client was configured for TLS")
        end
        if scheme == :https && insecure
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "https:// selects TLS, but this client was configured for plaintext")
        end

        authority = m[2].delete_suffix("/")
      end

      host, port = split_authority(endpoint, authority)
      if port.nil?
        if insecure
          example = scheme == :http ? "http://localhost:50051" : "localhost:50051"
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "a plaintext endpoint needs an explicit port, e.g. #{example}")
        end

        port = "443"
      end
      ["#{host}:#{port}", scheme]
    end

    # Parses a +dns:///host[:port]+ target (see .parse_endpoint).
    private_class_method def self.parse_dns_target(endpoint, insecure)
      m = endpoint.match(%r{\Adns:///(.*)\z}im)
      unless m
        raise InvalidEndpointError,
              invalid_endpoint_message(endpoint, "a dns target must be dns:///host:port (no DNS server authority)")
      end

      host, port = split_authority(endpoint, m[1])
      if port.nil?
        raise InvalidEndpointError, invalid_endpoint_message(endpoint, "a plaintext endpoint needs an explicit port, e.g. dns:///localhost:50051") if insecure

        port = "443"
      end
      ["dns:///#{host}:#{port}", :dns]
    end

    # Splits +authority+ into a host (IPv6 literals bracketed) and a port (nil
    # when absent), raising for anything that is not a plain host[:port].
    private_class_method def self.split_authority(endpoint, authority)
      raise InvalidEndpointError, invalid_endpoint_message(endpoint, "no host") if authority.empty?

      MALFORMED_AUTHORITY.each do |char, problem|
        raise InvalidEndpointError, invalid_endpoint_message(endpoint, problem) if authority.include?(char)
      end
      raise InvalidEndpointError, invalid_endpoint_message(endpoint, "whitespace is not allowed") if authority.match?(/\s/)

      host, port =
        if (m = authority.match(/\A(\[[^\]]*\])(?::(.*))?\z/))
          [m[1], m[2]]
        elsif authority.start_with?("[")
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "not a valid host and port")
        elsif authority.count(":") > 1
          # Only a bare IPv6 literal has several colons; it cannot carry a port
          # without brackets. Anything else (a gRPC "ipv4:" or "dns:" target) is
          # not a form this client accepts.
          raise InvalidEndpointError, invalid_endpoint_message(endpoint, "not a valid host and port") unless ipv6_literal?(authority)

          ["[#{authority}]", nil]
        elsif (idx = authority.index(":"))
          [authority[0...idx], authority[(idx + 1)..]]
        else
          [authority, nil]
        end
      raise InvalidEndpointError, invalid_endpoint_message(endpoint, "no host") if host.empty? || host == "[]"
      unless port.nil? || (port.match?(/\A\d{1,5}\z/) && port.to_i.between?(1, 65_535))
        raise InvalidEndpointError, invalid_endpoint_message(endpoint, "the port #{port.inspect} is not a number from 1 to 65535")
      end

      [host, port]
    end

    private_class_method def self.ipv6_literal?(text)
      IPAddr.new(text).ipv6?
    rescue IPAddr::Error
      false
    end

    private_class_method def self.invalid_endpoint_message(endpoint, reason)
      "spicedb: invalid endpoint #{endpoint.inspect}: #{reason}; #{ENDPOINT_FORMS}"
    end

    # Wraps an existing, caller-owned GRPC::Core::Channel (root DESIGN.md:
    # "Every client has a constructor from an existing connection"). The channel
    # already carries its transport security, so no credential or TLS argument
    # applies; +interceptors+ (for example a BearerTokenInterceptor on a
    # plaintext channel) are applied to every stub. #close never closes it.
    #
    # @param channel [GRPC::Core::Channel]
    # @param interceptors [Array<GRPC::ClientInterceptor>]
    # @raise [ArgumentError] if +channel+ is not a GRPC::Core::Channel
    def self.from_channel(channel, interceptors: [])
      raise ArgumentError, "spicedb: from_channel expects a GRPC::Core::Channel, got #{channel.class}" unless channel.is_a?(GRPC::Core::Channel)

      client = allocate
      client.instance_variable_set(:@channel, channel)
      client.instance_variable_set(:@owns_channel, false)
      opts = interceptors.empty? ? {} : { interceptors: interceptors.dup }
      client.send(:build_stubs, channel.target, opts)
      client
    end

    # Closes the underlying gRPC channel when this client created it. A channel
    # supplied to .from_channel stays open; it belongs to the caller.
    def close
      @channel&.close if @owns_channel
    end

    private def build_stubs(endpoint, stub_opts)
      stub_opts = stub_opts.merge(channel_override: @channel)
      @permissions = Authzed::Api::V1::PermissionsService::Stub.new(endpoint, nil, **stub_opts)
      @schema = Authzed::Api::V1::SchemaService::Stub.new(endpoint, nil, **stub_opts)
      @watch = Authzed::Api::V1::WatchService::Stub.new(endpoint, nil, **stub_opts)
      @experimental = Authzed::Api::V1::ExperimentalService::Stub.new(endpoint, nil, **stub_opts)
      @materialize_relationships = Authzed::Api::Materialize::V0::RelationshipsService::Stub.new(endpoint, nil, **stub_opts)
      @permission_sets = Authzed::Api::Materialize::V0::WatchPermissionSetsService::Stub.new(endpoint, nil, **stub_opts)
      @materialize = Authzed::Api::Materialize::V0::RoaringLookupResourcesService::Stub.new(endpoint, nil, **stub_opts)
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
