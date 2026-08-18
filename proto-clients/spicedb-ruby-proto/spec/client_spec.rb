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

    it "is idempotent -- calling it more than once does not raise" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)
      expect { client.close }.not_to raise_error
      expect { client.close }.not_to raise_error
      expect { client.close }.not_to raise_error
    end
  end

  # Regression coverage for the double-channel leak: the secure path used to
  # build a channel with bare credentials, then build a SECOND channel with
  # composed channel+call credentials and reassign @channel to it, leaking
  # the first (#close only closes whichever channel @channel currently
  # references). Asserting on GRPC::Core::Channel.new's call count -- not
  # just that construction doesn't raise -- is what would have caught this;
  # every stub still gets constructed either way.
  describe "channel construction" do
    it "creates exactly one underlying channel for a secure connection" do
      expect(GRPC::Core::Channel).to receive(:new).once.and_call_original
      described_class.new("localhost:50051", "test-token", insecure: false)
    end

    it "creates exactly one underlying channel for an insecure connection" do
      expect(GRPC::Core::Channel).to receive(:new).once.and_call_original
      described_class.new("localhost:50051", "test-token", insecure: true)
    end

    it "closing a secure client closes the same channel every stub was built with" do
      client = described_class.new("localhost:50051", "test-token", insecure: false)
      channel = client.instance_variable_get(:@channel)

      expect(channel).to receive(:close)
      client.close
    end
  end
end

# Authority-shifting targets: endpoints whose URI authority is not what a naive
# host:port split reads out of them. This exact set defeated the equivalent guard in
# this repo's C#, Rust, TypeScript and Java clients -- a last-colon (or first-"]") split
# reads a loopback host out of them, while those transports parsed the same string as a
# URI, took "127.0.0.1:443" for userinfo, and connected to evil.com, shipping the bearer
# token there in cleartext. grpc-ruby was not exploitable by them (C-core rejects
# "127.0.0.1:443@evil.com" outright with "Failed to parse port in name"), but the guard
# must fail closed on a target it cannot vouch for, and this fixture is what would catch
# a future edit that loosened the split here the way the C# one was loosened.
AUTHORITY_SHIFTING_ENDPOINTS = [
  "127.0.0.1:443@evil.com",
  "[::1]:443@evil.com",
  "[::1]:0@127.0.0.1:19999",
  "[localhost]:1@127.0.0.1:19999",
  "localhost@evil.com",
  "localhost/../evil.com",
  # Single-quoted: in a double-quoted Ruby string "#@evil" is instance-variable
  # interpolation, which would silently turn this fixture into "localhost.com".
  'localhost#@evil.com',
  "localhost?@evil.com",
  "localhost.",
  "localhost :50051",
  "127.0.0.1 :50051",
  # The port validation whose removal from the C# guard opened the bypass.
  "127.0.0.1:notaport"
].freeze

RSpec.describe "SpiceDBProto::Client.loopback_endpoint?" do
  loopback = %w[
    localhost:50051 LOCALHOST:50051 localhost
    127.0.0.1:50051 127.0.0.1 127.55.66.77:50051
    [::1]:50051 ::1
    unix:/var/run/spicedb.sock unix:///var/run/spicedb.sock
  ]
  loopback.each do |endpoint|
    it "treats #{endpoint.inspect} as loopback" do
      expect(SpiceDBProto::Client.loopback_endpoint?(endpoint)).to be true
    end
  end

  not_loopback = %w[
    example.com:443 staging.internal:443 10.0.0.5:50051 8.8.8.8:443 0.0.0.0:50051
    localhost.evil.com:443 127.0.0.1.evil.com:443 evil-localhost:443
  ]
  not_loopback.each do |endpoint|
    it "does not treat #{endpoint.inspect} as loopback" do
      expect(SpiceDBProto::Client.loopback_endpoint?(endpoint)).to be false
    end
  end

  AUTHORITY_SHIFTING_ENDPOINTS.each do |endpoint|
    it "does not treat authority-shifting #{endpoint.inspect} as loopback" do
      expect(SpiceDBProto::Client.loopback_endpoint?(endpoint)).to be false
    end
  end
end

# Regression coverage for root DESIGN.md, "RULE: Credentials over insecure
# transport require an explicit opt-in".
RSpec.describe "SpiceDBProto::Client insecure host guard" do
  # expect(...).not_to receive(:new) is what proves the credential never
  # reached the wire, not merely that an error was raised: GRPC::Core::Channel
  # is what the token would ride on, and BearerTokenInterceptor is what would
  # actually attach it to outgoing metadata. An implementation that built the
  # channel/interceptor and only THEN raised would fail these expectations
  # even though a bare `raise_error` assertion would still pass.
  it "refuses a non-loopback endpoint without the opt-in, before any channel or interceptor is created" do
    expect(GRPC::Core::Channel).not_to receive(:new)
    expect(SpiceDBProto::BearerTokenInterceptor).not_to receive(:new)

    expect {
      SpiceDBProto::Client.new("evil.example.com:1234", "super-secret-token", insecure: true)
    }.to raise_error(ArgumentError, /evil\.example\.com:1234/)
  end

  it "names the opt-in in the error message" do
    expect {
      SpiceDBProto::Client.new("evil.example.com:1234", "super-secret-token", insecure: true)
    }.to raise_error(ArgumentError, /allow_insecure_remote_credentials/)
  end

  it "allows a loopback endpoint with no opt-in, and actually carries the token" do
    client = SpiceDBProto::Client.new("localhost:50051", "test-token", insecure: true)
    interceptor = client.instance_variable_get(:@interceptor)

    metadata = {}
    interceptor.request_response(request: nil, call: nil, method: nil, metadata: metadata) { nil }

    expect(metadata["authorization"]).to eq("Bearer test-token")
  end

  it "allows a non-loopback endpoint when allow_insecure_remote_credentials is true, and sends the token" do
    client = SpiceDBProto::Client.new(
      "evil.example.com:1234", "remote-token", insecure: true, allow_insecure_remote_credentials: true
    )
    interceptor = client.instance_variable_get(:@interceptor)

    metadata = {}
    interceptor.request_response(request: nil, call: nil, method: nil, metadata: metadata) { nil }

    expect(metadata["authorization"]).to eq("Bearer remote-token")
  end
end

# The regression test for the loopback-guard bypass. Asserting only that an error is
# raised would be satisfied by an implementation that builds the channel, sends the
# token, and raises afterwards -- so these assert on the transport instead, exactly as
# the "insecure host guard" specs above do: GRPC::Core::Channel is what the token would
# ride on and BearerTokenInterceptor is what would attach it, and neither may be
# constructed at all for a refused endpoint.
RSpec.describe "SpiceDBProto::Client authority-shifting endpoint guard" do
  AUTHORITY_SHIFTING_ENDPOINTS.each do |endpoint|
    it "refuses #{endpoint.inspect} before any channel or interceptor is created" do
      expect(GRPC::Core::Channel).not_to receive(:new)
      expect(SpiceDBProto::BearerTokenInterceptor).not_to receive(:new)

      expect do
        SpiceDBProto::Client.new(endpoint, "super-secret-token", insecure: true)
      end.to raise_error(ArgumentError, /allow_insecure_remote_credentials/)
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
