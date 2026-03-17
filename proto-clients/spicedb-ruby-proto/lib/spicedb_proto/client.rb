# frozen_string_literal: true

require "grpc"

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
    def initialize(endpoint, token, insecure: false)
      if insecure
        credentials = :this_channel_is_insecure
      else
        credentials = GRPC::Core::ChannelCredentials.new
      end

      @channel = GRPC::Core::Channel.new(endpoint, {}, credentials)

      call_creds = GRPC::Core::CallCredentials.new(proc { |_context|
        { "authorization" => "Bearer #{token}" }
      })

      # For secure channels, compose channel + call credentials.
      # For insecure channels, pass call credentials via the interceptor instead.
      if insecure
        @interceptor = BearerTokenInterceptor.new(token)
        stub_opts = { channel_override: @channel, interceptors: [@interceptor] }
      else
        combined = credentials.compose(call_creds)
        @channel = GRPC::Core::Channel.new(endpoint, {}, combined)
        stub_opts = { channel_override: @channel }
      end

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
