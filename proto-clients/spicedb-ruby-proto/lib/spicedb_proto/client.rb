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
    # @raise [ArgumentError] if +insecure+ is true, +endpoint+ is not loopback, and
    #   +allow_insecure_remote_credentials+ is false -- raised before any channel,
    #   credential, or connection is created, so the token can never reach the wire for a
    #   rejected combination.
    def initialize(endpoint, token, insecure: false, allow_insecure_remote_credentials: false)
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
        credentials = GRPC::Core::ChannelCredentials.new.compose(call_creds)
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

    # Reports whether a gRPC target string names a loopback destination: the literal
    # hostname "localhost", an IP in 127.0.0.0/8, the IPv6 loopback ::1, or a unix
    # domain socket target (a "unix:" prefix). A unix socket never leaves the host's
    # kernel, so it is loopback for this check even though it has no IP at all.
    #
    # This is the exemption in root DESIGN.md, "RULE: Credentials over insecure
    # transport require an explicit opt-in": loopback is the reason insecure: true
    # exists at all (local development, docker-compose, CI), so it must keep working
    # with no extra ceremony. Anything else requires
    # allow_insecure_remote_credentials: true -- see #initialize above.
    #
    # @api private
    def self.loopback_endpoint?(endpoint)
      return true if endpoint.start_with?("unix:")

      host =
        if (m = endpoint.match(/\A\[(.+)\]:\d+\z/))
          m[1] # "[::1]:50051" -> "::1"
        elsif (m = endpoint.match(/\A\[(.+)\]\z/))
          m[1] # "[::1]" -> "::1"
        elsif endpoint.count(":") > 1
          endpoint # bare IPv6 (e.g. "::1") -- no port is possible without brackets
        elsif (idx = endpoint.rindex(":"))
          endpoint[0...idx] # "host:port"
        else
          endpoint # bare host, no port
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
