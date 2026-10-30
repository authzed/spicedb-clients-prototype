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

    stub_class = Class.new do
      attr_reader :host, :channel

      def initialize(host, creds = nil, **kwargs)
        @host = host
        @channel = kwargs[:channel_override]
      end
    end

    %w[PermissionsService SchemaService WatchService ExperimentalService].each do |svc|
      unless Authzed::Api::V1.const_defined?(svc)
        svc_mod = Module.new
        svc_mod.const_set(:Stub, stub_class)
        Authzed::Api::V1.const_set(svc, svc_mod)
      end
    end

    # RoaringLookupResourcesService lives in a separate generated package
    # (authzed.api.materialize.v0) from the four above (authzed.api.v1).
    Authzed.const_set(:Api, Module.new) unless defined?(::Authzed::Api)
    Authzed::Api.const_set(:Materialize, Module.new) unless defined?(::Authzed::Api::Materialize)
    Authzed::Api::Materialize.const_set(:V0, Module.new) unless defined?(::Authzed::Api::Materialize::V0)
    %w[RoaringLookupResourcesService RelationshipsService WatchPermissionSetsService].each do |service|
      next if Authzed::Api::Materialize::V0.const_defined?(service)

      svc_mod = Module.new
      svc_mod.const_set(:Stub, stub_class)
      Authzed::Api::Materialize::V0.const_set(service, svc_mod)
    end
  end

  describe "#initialize" do
    it "creates all seven service stubs with insecure channel" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)

      expect(client.permissions).to be_a(Authzed::Api::V1::PermissionsService::Stub)
      expect(client.schema).to be_a(Authzed::Api::V1::SchemaService::Stub)
      expect(client.watch).to be_a(Authzed::Api::V1::WatchService::Stub)
      expect(client.experimental).to be_a(Authzed::Api::V1::ExperimentalService::Stub)
      expect(client.materialize).to be_a(Authzed::Api::Materialize::V0::RoaringLookupResourcesService::Stub)
      expect(client.materialize_relationships).to be_a(Authzed::Api::Materialize::V0::RelationshipsService::Stub)
      expect(client.permission_sets).to be_a(Authzed::Api::Materialize::V0::WatchPermissionSetsService::Stub)
      services = [client.permissions, client.schema, client.watch, client.experimental,
                  client.materialize, client.materialize_relationships, client.permission_sets]
      expect(services.map(&:channel).uniq).to eq([client.permissions.channel])
    end

    it "sets the endpoint on each stub" do
      client = described_class.new("localhost:50051", "test-token", insecure: true)

      expect(client.permissions.host).to eq("localhost:50051")
      expect(client.schema.host).to eq("localhost:50051")
    end
  end

  describe ".from_channel" do
    it "builds every stub on the caller's channel and never closes it" do
      channel = GRPC::Core::Channel.new("localhost:50051", {}, :this_channel_is_insecure)
      client = described_class.from_channel(channel)
      services = [client.permissions, client.schema, client.watch, client.experimental,
                  client.materialize, client.materialize_relationships, client.permission_sets]
      expect(services.map(&:channel).uniq).to eq([channel])
      client.close
      expect { channel.connectivity_state }.not_to raise_error
      channel.close
    end

    it "rejects anything but a channel" do
      expect { described_class.from_channel("localhost:50051") }.to raise_error(ArgumentError)
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
    UNIX:/var/run/spicedb.sock Unix:///var/run/spicedb.sock
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
    }.to raise_error(ArgumentError, /to allow it, pass both insecure: true and allow_insecure_remote_credentials: true; otherwise use TLS \(insecure: false\)/)
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
      end.to raise_error(SpiceDBProto::InvalidEndpointError, /invalid endpoint/)
    end
  end
end

# Root DESIGN.md, "Accept the endpoint forms deployments use, or reject them at
# construction". Every refusal here happens before a channel exists.
RSpec.describe "SpiceDBProto::Client endpoint forms" do
  def parse(endpoint, insecure:) = SpiceDBProto::Client.parse_endpoint(endpoint, insecure: insecure)

  {
    "127.0.0.1:50101" => ["127.0.0.1:50101", nil],
    "http://127.0.0.1:50101" => ["127.0.0.1:50101", :http],
    "HTTP://127.0.0.1:50101/" => ["127.0.0.1:50101", :http],
    "http://[::1]:50051" => ["[::1]:50051", :http],
    "[::1]:50051" => ["[::1]:50051", nil],
    "unix:///var/run/spicedb.sock" => ["unix:///var/run/spicedb.sock", nil],
    "dns:///localhost:50051" => ["dns:///localhost:50051", :dns],
    "DNS:///127.0.0.1:50051" => ["dns:///127.0.0.1:50051", :dns],
    "dns:///[::1]:50051" => ["dns:///[::1]:50051", :dns]
  }.each do |endpoint, expected|
    it "parses plaintext #{endpoint.inspect} to #{expected.inspect}" do
      expect(parse(endpoint, insecure: true)).to eq(expected)
    end
  end

  {
    "spicedb.example.com:443" => ["spicedb.example.com:443", nil],
    "spicedb.example.com" => ["spicedb.example.com:443", nil],
    "https://example.com" => ["example.com:443", :https],
    "https://example.com:8443" => ["example.com:8443", :https],
    "https://[::1]" => ["[::1]:443", :https],
    "::1" => ["[::1]:443", nil],
    "dns:///spicedb.example.com" => ["dns:///spicedb.example.com:443", :dns],
    "dns:///spicedb.example.com:8443" => ["dns:///spicedb.example.com:8443", :dns]
  }.each do |endpoint, expected|
    it "parses TLS #{endpoint.inspect} to #{expected.inspect}" do
      expect(parse(endpoint, insecure: false)).to eq(expected)
    end
  end

  ["localhost", "127.0.0.1", "[::1]", "::1", "http://localhost"].each do |endpoint|
    it "rejects plaintext #{endpoint.inspect} without a port, naming host:port" do
      expect(GRPC::Core::Channel).not_to receive(:new)
      expect { SpiceDBProto::Client.new(endpoint, "t", insecure: true) }
        .to raise_error(SpiceDBProto::InvalidEndpointError, /a plaintext endpoint needs an explicit port, e\.g\. (http:\/\/)?localhost:50051/)
    end
  end

  {
    "ipv4:127.0.0.1:50051" => /not a valid host and port/,
    "http://127.0.0.1:50101/x" => /a path is not supported/,
    "http://127.0.0.1:50101?a=b" => /a query is not supported/,
    "http://127.0.0.1:50101#frag" => /a fragment is not supported/,
    "http://u@127.0.0.1:50101" => /userinfo is not supported/,
    "ftp://host:1" => /unsupported scheme "ftp"/,
    "dns://8.8.8.8/host:1" => /a dns target must be dns:\/\/\/host:port/,
    "dns:host:1" => /a dns target must be dns:\/\/\/host:port/,
    "dns:///host:1/x" => /a path is not supported/,
    "dns:///u@host:1" => /userinfo is not supported/,
    "dns:///host:abc" => /port "abc" is not a number/,
    "dns:///" => /no host/,
    "xds:///host:1" => /unsupported scheme "xds"/,
    "http://127.0.0.1:http" => /port "http" is not a number/,
    "localhost:abc" => /port "abc" is not a number/,
    "http://" => /no host/,
    "http://:50051" => /no host/
  }.each do |endpoint, reason|
    it "rejects #{endpoint.inspect} naming the problem and the accepted forms" do
      expect(GRPC::Core::Channel).not_to receive(:new)
      expect { SpiceDBProto::Client.new(endpoint, "t", insecure: true) }
        .to raise_error(SpiceDBProto::InvalidEndpointError,
                        /invalid endpoint .*#{reason.source}.*expected host:port, http:\/\/host:port/)
    end
  end

  it "reports a malformed remote URL as malformed, not as remote plaintext" do
    expect { SpiceDBProto::Client.new("http://u@example.com:50051", "t", insecure: true) }
      .to raise_error(SpiceDBProto::InvalidEndpointError, /userinfo/)
  end

  it "runs the loopback check on the parsed host" do
    expect { SpiceDBProto::Client.validate_connection!("http://127.0.0.1:50101", insecure: true) }.not_to raise_error
    expect { SpiceDBProto::Client.validate_connection!("http://example.com:50051", insecure: true) }
      .to raise_error(SpiceDBProto::InsecureRemoteHostError, /"http:\/\/example\.com:50051".*allow_insecure_remote_credentials/)
  end

  it "rejects a plaintext dns target without a port" do
    expect { SpiceDBProto::Client.validate_connection!("dns:///localhost", insecure: true) }
      .to raise_error(SpiceDBProto::InvalidEndpointError, /needs an explicit port, e\.g\. dns:\/\/\/localhost:50051/)
  end

  it "runs the loopback check on a dns target's host" do
    %w[dns:///localhost:50051 dns:///127.0.0.1:50051 dns:///[::1]:50051].each do |endpoint|
      expect(SpiceDBProto::Client.validate_connection!(endpoint, insecure: true)).to eq(endpoint)
    end
    expect { SpiceDBProto::Client.validate_connection!("dns:///example.com:50051", insecure: true) }
      .to raise_error(SpiceDBProto::InsecureRemoteHostError, /"dns:\/\/\/example\.com:50051"/)
    expect(SpiceDBProto::Client.validate_connection!("dns:///example.com:50051", insecure: true,
                                                     allow_insecure_remote_credentials: true))
      .to eq("dns:///example.com:50051")
  end

  it "dials a dns target unchanged" do
    allow_any_instance_of(SpiceDBProto::Client).to receive(:build_stubs)
    expect(GRPC::Core::Channel).to receive(:new).with("dns:///example.com:443", {}, anything).and_call_original
    SpiceDBProto::Client.new("dns:///example.com", "t").close
  end

  it "rejects a URL scheme that disagrees with the transport" do
    expect { SpiceDBProto::Client.validate_connection!("https://example.com", insecure: true) }
      .to raise_error(SpiceDBProto::InvalidEndpointError, /https:\/\/ selects TLS/)
    expect { SpiceDBProto::Client.validate_connection!("http://127.0.0.1:50101", insecure: false) }
      .to raise_error(SpiceDBProto::InvalidEndpointError, /http:\/\/ selects plaintext/)
  end

  it "dials the host:port a URL names" do
    allow_any_instance_of(SpiceDBProto::Client).to receive(:build_stubs)
    expect(GRPC::Core::Channel).to receive(:new).with("example.com:443", {}, anything).and_call_original
    SpiceDBProto::Client.new("https://example.com", "t").close
  end
end

# F4: an authority-bearing unix target is guard-loopback (the prefix check is
# deliberately case-insensitive and does not inspect the authority), so what
# actually stops it is the transport. C-core refuses to build a usable channel
# for ANY casing -- "authority-based URIs not supported by the unix scheme" ->
# "the target uri is not valid" -- so nothing is dialed and no token moves.
#
# This pins that. It is NOT a red-first regression test: it passes before and
# after, because it asserts existing transport behaviour rather than a change.
# Its job is to fail loudly if C-core ever starts accepting authority-bearing
# unix targets, at which point the guard's permissiveness would become a real
# leak instead of a harmless over-permission.
RSpec.describe "SpiceDBProto authority-bearing unix targets are refused by the transport" do
  ["unix://evil.com/tmp/x.sock", "UNIX://evil.com/tmp/x.sock"].each do |endpoint|
    it "cannot complete an RPC to #{endpoint.inspect}" do
      # Precondition: the guard alone does not stop these.
      expect(SpiceDBProto::Client.loopback_endpoint?(endpoint)).to be true

      channel = GRPC::Core::Channel.new(endpoint, {}, :this_channel_is_insecure)
      stub = GRPC::ClientStub.new(endpoint, :this_channel_is_insecure, channel_override: channel)
      begin
        expect do
          stub.request_response("/demo.Svc/Method", "", ->(x) { x }, ->(x) { x },
                                deadline: Time.now + 5)
        end.to raise_error(GRPC::BadStatus, /target uri is not valid/)
      ensure
        channel.close
      end
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
