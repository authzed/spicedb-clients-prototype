# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'

RSpec.describe SpiceDB::Client do
  # Root DESIGN.md, "Accept the endpoint forms deployments use, or reject them
  # at construction": no endpoint builds a client whose every call fails.
  describe 'endpoint forms' do
    it 'accepts host:port and http://host:port on new_plaintext' do
      %w[127.0.0.1:50101 http://127.0.0.1:50101 http://localhost:50101/].each do |endpoint|
        expect { described_class.new_plaintext(endpoint, 'testtoken').close }.not_to raise_error
      end
    end

    it 'accepts https://host with or without a port on new_system_tls' do
      %w[https://example.com https://example.com:8443 grpc.example.com:443 grpc.example.com].each do |endpoint|
        expect { described_class.new_system_tls(endpoint, 'testtoken').close }.not_to raise_error
      end
    end

    it 'accepts dns:///host:port gRPC targets, with the loopback check on the host' do
      %w[dns:///localhost:50101 dns:///127.0.0.1:50101 DNS:///[::1]:50101].each do |endpoint|
        expect { described_class.new_plaintext(endpoint, 'testtoken').close }.not_to raise_error
      end
      %w[dns:///spicedb.example.com dns:///spicedb.example.com:8443].each do |endpoint|
        expect { described_class.new_system_tls(endpoint, 'testtoken').close }.not_to raise_error
      end
      expect { described_class.new_plaintext('dns:///example.com:50051', 'testtoken') }
        .to raise_error(SpiceDB::InvalidArgumentError, /RemoteCredentials::ALLOW_PLAINTEXT/)
    end

    {
      'dns:///localhost' => %r{a plaintext endpoint needs an explicit port, e\.g\. dns:///localhost:50051},
      'dns://8.8.8.8/localhost:50051' => %r{a dns target must be dns:///host:port},
      'dns:localhost:50051' => %r{a dns target must be dns:///host:port},
      'dns:///localhost:50051/x' => /a path is not supported/,
      'xds:///localhost:50051' => /unsupported scheme "xds"/,
      'ipv4:127.0.0.1:50051' => /not a valid host and port/,
      'http://localhost' => %r{a plaintext endpoint needs an explicit port, e\.g\. http://localhost:50051},
      'localhost' => /a plaintext endpoint needs an explicit port, e\.g\. localhost:50051/,
      '127.0.0.1' => /a plaintext endpoint needs an explicit port/,
      '[::1]' => /a plaintext endpoint needs an explicit port/,
      '::1' => /a plaintext endpoint needs an explicit port/,
      'http://127.0.0.1:50101/x' => /a path is not supported/,
      'http://u@127.0.0.1:50101' => /userinfo is not supported/,
      'ftp://host:1' => /unsupported scheme "ftp"/,
      'http://127.0.0.1:abc' => /is not a number/,
      'http://u@example.com:50051' => /userinfo is not supported/
    }.each do |endpoint, reason|
      it "rejects #{endpoint.inspect} at construction, naming the problem and the accepted forms" do
        expect { described_class.new_plaintext(endpoint, 'testtoken') }
          .to raise_error(SpiceDB::InvalidArgumentError, /#{reason.source}.*expected host:port.*dns:.*unix:/)
      end
    end

    it 'rejects a scheme that disagrees with the constructor' do
      expect { described_class.new_plaintext('https://example.com', 'testtoken') }
        .to raise_error(SpiceDB::InvalidArgumentError, %r{https:// selects TLS})
      expect { described_class.new_system_tls('http://127.0.0.1:50101', 'testtoken') }
        .to raise_error(SpiceDB::InvalidArgumentError, %r{http:// selects plaintext})
    end

    it 'names every option needed to allow remote plaintext, as a caller writes them' do
      expect { described_class.new_plaintext('spicedb:50051', 'testtoken') }.to raise_error(
        SpiceDB::InvalidArgumentError,
        'spicedb: refusing to send credentials in plaintext to non-loopback endpoint "spicedb:50051". ' \
        'To allow it, pass both the plaintext transport and the remote-credentials opt-in: ' \
        'SpiceDB::Client.new_plaintext("spicedb:50051", token, remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT) ' \
        '(or SpiceDB::Client.new(transport: SpiceDB::TransportSecurity::PLAINTEXT, ' \
        'remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT, ...)). ' \
        'Otherwise use TLS: SpiceDB::Client.new_system_tls'
      )
    end

    it 'connects to a Compose service name with the explicit opt-in' do
      expect do
        described_class.new_plaintext('spicedb:50051', 'testtoken',
                                      remote_credentials: SpiceDB::RemoteCredentials::ALLOW_PLAINTEXT).close
      end.not_to raise_error
    end

    it 'applies the remote-plaintext policy to the parsed host of a URL' do
      expect { described_class.new_plaintext('http://example.com:50051', 'testtoken') }
        .to raise_error(SpiceDB::InvalidArgumentError, /RemoteCredentials::ALLOW_PLAINTEXT/)
    end
  end
end
