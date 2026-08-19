# frozen_string_literal: true

require "rspec"
require "grpc"

# Same isolation as client_spec.rb: lib/gen/ is empty until buf generate runs,
# so the client module is loaded directly rather than through the entrypoint.
require_relative "../lib/spicedb_proto/client"

# Caller-supplied TLS trust material at the proto tier.
#
# The handshake proof lives in the idiomatic client's
# spec/client_custom_tls_spec.rb, which drives a real GRPC::RpcServer over TLS
# through this constructor. What these specs pin is the wiring and the
# refusals: that the material actually reaches
# GRPC::Core::ChannelCredentials in the C-core's argument order, that the
# default secure path is still the zero-argument delegation root DESIGN.md's
# "RULE: A system-TLS constructor must reach a real server" requires, and that
# none of it opens a route around the credential guard.
RSpec.describe "SpiceDBProto::Client TLS material" do
  before(:all) do
    mod = Module.new
    Object.const_set(:Authzed, Module.new) unless defined?(::Authzed)
    Authzed.const_set(:Api, Module.new) unless defined?(::Authzed::Api)
    Authzed::Api.const_set(:V1, Module.new) unless defined?(::Authzed::Api::V1)

    %w[PermissionsService SchemaService WatchService ExperimentalService].each do |svc|
      next if Authzed::Api::V1.const_defined?(svc)

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
    mod
  end

  describe "credential construction" do
    # The C-core's parameter order is (pem_root_certs, pem_private_key,
    # pem_cert_chain) -- key BEFORE certificate chain, the reverse of how a
    # caller names them. Getting that backwards produces a client that fails
    # only at handshake time, deep inside the C-core, so it is pinned here
    # explicitly rather than left to the end-to-end spec to catch.
    it "hands the material to ChannelCredentials in the C-core's order" do
      composed = instance_double(GRPC::Core::ChannelCredentials)
      creds = instance_double(GRPC::Core::ChannelCredentials, compose: composed)
      expect(GRPC::Core::ChannelCredentials).to receive(:new)
        .with("ca-pem", "key-pem", "cert-pem").and_return(creds)
      allow(GRPC::Core::Channel).to receive(:new)

      SpiceDBProto::Client.new(
        "spicedb.example.com:443", "token",
        ca_cert: "ca-pem", client_cert: "cert-pem", client_key: "key-pem"
      )
    end

    # Root DESIGN.md, "RULE: A system-TLS constructor must reach a real
    # server", clause 1 prohibits this library selecting its own root set. All
    # three arguments nil is the same call the zero-argument form makes, so the
    # default path is still pure delegation to whatever gRPC trusts -- pinned
    # because a future edit reaching for "a sensible default bundle" is exactly
    # the defect that clause exists to catch.
    it "passes nil for every argument when no material is supplied" do
      composed = instance_double(GRPC::Core::ChannelCredentials)
      creds = instance_double(GRPC::Core::ChannelCredentials, compose: composed)
      expect(GRPC::Core::ChannelCredentials).to receive(:new)
        .with(nil, nil, nil).and_return(creds)
      allow(GRPC::Core::Channel).to receive(:new)

      SpiceDBProto::Client.new("spicedb.example.com:443", "token")
    end
  end

  describe "refusals" do
    # Root DESIGN.md, "RULE: Credentials over insecure transport require an
    # explicit opt-in". A plaintext channel performs no handshake, so the
    # material would be discarded and the bearer token -- which this client
    # hands to a BearerTokenInterceptor on that path -- would go out in
    # cleartext, behind a call site reading as though TLS were configured.
    %i[ca_cert client_cert client_key].each do |field|
      it "refuses insecure combined with #{field}, rather than ignoring it" do
        expect(GRPC::Core::Channel).not_to receive(:new)

        expect do
          SpiceDBProto::Client.new("localhost:50051", "token", insecure: true, field => "pem")
        end.to raise_error(ArgumentError, /insecure: true and TLS material/)
      end
    end

    it "still refuses a non-loopback insecure endpoint first, with material in hand" do
      # Supplying a CA must not become a second construction path that skips
      # the credential guard -- and the credential-leak message, not the TLS
      # one, is what the caller sees.
      expect do
        SpiceDBProto::Client.new("evil.example.com:1234", "token", insecure: true, ca_cert: "pem")
      end.to raise_error(ArgumentError, /allow_insecure_remote_credentials/)
    end

    it "refuses a client certificate without its key" do
      expect(GRPC::Core::Channel).not_to receive(:new)

      expect do
        SpiceDBProto::Client.new("spicedb.example.com:443", "token", client_cert: "pem")
      end.to raise_error(ArgumentError, /client_key/)
    end

    it "refuses a client key without its certificate" do
      expect(GRPC::Core::Channel).not_to receive(:new)

      expect do
        SpiceDBProto::Client.new("spicedb.example.com:443", "token", client_key: "pem")
      end.to raise_error(ArgumentError, /client_cert/)
    end
  end
end
