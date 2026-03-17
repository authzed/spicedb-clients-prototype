# frozen_string_literal: true

require "rspec"
require "grpc"

# We cannot load the full spicedb_proto entrypoint because lib/gen/ is empty
# until buf generate runs. Instead, test the client module in isolation.
require_relative "../lib/spicedb_proto/client"

RSpec.describe SpiceDBProto::Client do
  # Since generated stubs are not present yet, we create minimal stub classes
  # that quack like gRPC service stubs so we can verify the client wiring.

  before(:all) do
    # Define fake service stub classes in the expected namespace.
    mod = Module.new
    Object.const_set(:Authzed, Module.new) unless defined?(::Authzed)
    Authzed.const_set(:Api, Module.new) unless defined?(::Authzed::Api)
    Authzed::Api.const_set(:V1, Module.new) unless defined?(::Authzed::Api::V1)

    %w[PermissionsService SchemaService WatchService ExperimentalService].each do |svc|
      unless Authzed::Api::V1.const_defined?(svc)
        svc_mod = Module.new
        stub_class = Class.new do
          attr_reader :host, :channel

          def initialize(host, creds = nil, **kwargs)
            @host = host
            @channel = kwargs[:channel_override]
          end
        end
        svc_mod.const_set(:Stub, stub_class)
        Authzed::Api::V1.const_set(svc, svc_mod)
      end
    end
  end

  describe "#initialize" do
    it "creates all four service stubs with insecure channel" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)

      expect(client.permissions).to be_a(Authzed::Api::V1::PermissionsService::Stub)
      expect(client.schema).to be_a(Authzed::Api::V1::SchemaService::Stub)
      expect(client.watch).to be_a(Authzed::Api::V1::WatchService::Stub)
      expect(client.experimental).to be_a(Authzed::Api::V1::ExperimentalService::Stub)
    end

    it "sets the endpoint on each stub" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)

      expect(client.permissions.host).to eq("localhost:50051")
      expect(client.schema.host).to eq("localhost:50051")
    end
  end

  describe "#close" do
    it "does not raise when called" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)
      expect { client.close }.not_to raise_error
    end
  end
end

RSpec.describe SpiceDBProto::BearerTokenInterceptor do
  it "merges authorization metadata" do
    interceptor = described_class.new("my-token")
    metadata = {}

    # Simulate the interceptor being called by providing a block
    interceptor.request_response(
      request: nil, call: nil, method: nil, metadata: metadata
    ) { nil }

    expect(metadata["authorization"]).to eq("Bearer my-token")
  end
end
