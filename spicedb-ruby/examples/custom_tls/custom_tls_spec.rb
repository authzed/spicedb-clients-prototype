# frozen_string_literal: true

require_relative '../spec_helper'
require 'spicedb_proto' # for the generated PermissionsService the example's own server implements
require 'grpc'
require 'openssl'

# Example: Connecting to a SpiceDB fronted by a private CA (and mutual TLS)
#
# By default SpiceDB::Client.new_system_tls trusts whatever gRPC trusts, which
# is a roots.pem compiled into gRPC's C-core -- NOT the host's certificate
# store. So a SpiceDB terminated behind a corporate or cluster-internal CA is
# unreachable until you hand the client that CA yourself:
#
#   SpiceDB::Client.new_custom_tls(
#     'spicedb.internal:443',
#     ENV.fetch('SPICEDB_TOKEN'),
#     ca_cert: File.read('/etc/ssl/certs/internal-ca.pem')
#   ) do |client|
#     ...
#   end
#
# Add client_cert: and client_key: where the server requires mutual TLS -- both
# halves together; either alone is refused.
#
# Unlike the other examples here, this one does not use the shared SpiceDB at
# localhost:50051 (hence :no_spicedb): a plaintext server has nothing to
# demonstrate about trust material. It brings up its own TLS-terminated gRPC
# endpoint with a throwaway CA instead, so what runs below is a real handshake
# against a certificate no public root set would accept.

# Stands in for a TLS-terminated SpiceDB.
class ExampleTlsPermissionsService < Authzed::Api::V1::PermissionsService::Service
  def check_bulk_permissions(request, _call)
    Authzed::Api::V1::CheckBulkPermissionsResponse.new(
      pairs: request.items.map do |item|
        Authzed::Api::V1::CheckBulkPermissionsPair.new(
          request: item,
          item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
            permissionship: :PERMISSIONSHIP_HAS_PERMISSION
          )
        )
      end
    )
  end
end

RSpec.describe 'CustomTLS', :no_spicedb do
  # A throwaway PKI, standing in for whatever your deployment actually uses.
  # In production these are files on disk or keys in a mounted secret; the only
  # thing the client needs is their PEM contents.
  def self.pki
    @pki ||= begin
      ca_key = OpenSSL::PKey::RSA.new(2048)
      ca = issue(subject: '/CN=example internal CA', serial: 1, key: ca_key,
                 issuer_cert: nil, issuer_key: ca_key,
                 extensions: [['basicConstraints', 'CA:TRUE', true],
                              ['keyUsage', 'keyCertSign, cRLSign', true]])
      server_key = OpenSSL::PKey::RSA.new(2048)
      # SAN, not CN: gRPC's C-core embeds BoringSSL, which ignores the common
      # name entirely.
      server = issue(subject: '/CN=localhost', serial: 2, key: server_key,
                     issuer_cert: ca, issuer_key: ca_key,
                     extensions: [['basicConstraints', 'CA:FALSE', true],
                                  ['keyUsage', 'digitalSignature, keyEncipherment', true],
                                  ['extendedKeyUsage', 'serverAuth', false],
                                  ['subjectAltName', 'DNS:localhost,IP:127.0.0.1', false]])
      client_key = OpenSSL::PKey::RSA.new(2048)
      client = issue(subject: '/CN=example client', serial: 3, key: client_key,
                     issuer_cert: ca, issuer_key: ca_key,
                     extensions: [['basicConstraints', 'CA:FALSE', true],
                                  ['keyUsage', 'digitalSignature, keyEncipherment', true],
                                  ['extendedKeyUsage', 'clientAuth', false]])
      { ca: ca.to_pem, server_cert: server.to_pem, server_key: server_key.to_pem,
        client_cert: client.to_pem, client_key: client_key.to_pem }
    end
  end

  def self.issue(subject:, serial:, key:, issuer_cert:, issuer_key:, extensions:)
    cert = OpenSSL::X509::Certificate.new
    cert.version = 2
    cert.serial = serial
    cert.subject = OpenSSL::X509::Name.parse(subject)
    cert.issuer = issuer_cert ? issuer_cert.subject : cert.subject
    cert.public_key = key.public_key
    cert.not_before = Time.now - 60
    cert.not_after = Time.now + 3600
    factory = OpenSSL::X509::ExtensionFactory.new
    factory.subject_certificate = cert
    factory.issuer_certificate = issuer_cert || cert
    extensions.each { |name, value, critical| cert.add_extension(factory.create_extension(name, value, critical)) }
    cert.sign(issuer_key, OpenSSL::Digest.new('SHA256'))
    cert
  end

  def start_tls_server(require_client_auth: false)
    pki = self.class.pki
    credentials = GRPC::Core::ServerCredentials.new(
      require_client_auth ? pki[:ca] : nil,
      [{ private_key: pki[:server_key], cert_chain: pki[:server_cert] }],
      require_client_auth
    )
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', credentials)
    server.handle(ExampleTlsPermissionsService.new)
    Thread.new { server.run }
    server.wait_till_running(5)
    [server, port]
  end

  let(:pki) { self.class.pki }
  let(:rel) { SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice') }

  it 'reaches a SpiceDB behind a private CA' do
    server, port = start_tls_server
    begin
      SpiceDB::Client.new_custom_tls("localhost:#{port}", SPICEDB_TOKEN,
                                     ca_cert: pki[:ca], default_timeout: 10) do |client|
        results = client.check_permissions(SpiceDB::Consistency.full, 'view', rel)
        expect(results.first.has_permission?).to be true
      end
    ensure
      server.stop
    end
  end

  it 'presents a client certificate where the server requires mutual TLS' do
    server, port = start_tls_server(require_client_auth: true)
    begin
      SpiceDB::Client.new_custom_tls("localhost:#{port}", SPICEDB_TOKEN,
                                     ca_cert: pki[:ca], client_cert: pki[:client_cert],
                                     client_key: pki[:client_key], default_timeout: 10) do |client|
        results = client.check_permissions(SpiceDB::Consistency.full, 'view', rel)
        expect(results.first.has_permission?).to be true
      end
    ensure
      server.stop
    end
  end

  it 'refuses to be a disguised new_system_tls' do
    # Naming a constructor for custom trust material and then silently using
    # gRPC's compiled-in roots would be a quiet way to believe a private CA was
    # configured when it was not.
    expect do
      SpiceDB::Client.new_custom_tls('spicedb.internal:443', SPICEDB_TOKEN)
    end.to raise_error(ArgumentError, /new_system_tls/)
  end
end
