# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'
require 'openssl'

# A throwaway CA and the leaves it signs, built in-process.
#
# Generated rather than committed: a checked-in certificate expires, and an
# expiry is a test that starts failing for a reason unrelated to the code it
# covers. Built with Ruby's own OpenSSL bindings rather than by shelling out,
# so the fixture has no dependency outside the gem set.
module TestPki
  module_function

  def ca
    @ca ||= begin
      key = OpenSSL::PKey::RSA.new(2048)
      cert = base_cert('/CN=spicedb-ruby test CA', key.public_key, 1)
      cert.issuer = cert.subject
      factory = extension_factory(cert, cert)
      cert.add_extension(factory.create_extension('basicConstraints', 'CA:TRUE', true))
      cert.add_extension(factory.create_extension('keyUsage', 'keyCertSign, cRLSign', true))
      cert.sign(key, OpenSSL::Digest.new('SHA256'))
      { key: key, cert: cert }
    end
  end

  # SAN, not CN: gRPC's C-core embeds BoringSSL, which ignores the common name
  # entirely -- a certificate without a matching SAN fails verification even
  # against its own CA.
  def server
    @server ||= leaf('/CN=localhost', 2, 'serverAuth', 'DNS:localhost,IP:127.0.0.1')
  end

  def client
    @client ||= leaf('/CN=spicedb test client', 3, 'clientAuth', nil)
  end

  def leaf(subject, serial, usage, san)
    key = OpenSSL::PKey::RSA.new(2048)
    cert = base_cert(subject, key.public_key, serial)
    cert.issuer = ca[:cert].subject
    factory = extension_factory(cert, ca[:cert])
    cert.add_extension(factory.create_extension('basicConstraints', 'CA:FALSE', true))
    cert.add_extension(
      factory.create_extension('keyUsage', 'digitalSignature, keyEncipherment', true)
    )
    cert.add_extension(factory.create_extension('extendedKeyUsage', usage, false))
    cert.add_extension(factory.create_extension('subjectAltName', san, false)) if san
    cert.sign(ca[:key], OpenSSL::Digest.new('SHA256'))
    { key: key, cert: cert }
  end

  def base_cert(subject, public_key, serial)
    cert = OpenSSL::X509::Certificate.new
    cert.version = 2
    cert.serial = serial
    cert.subject = OpenSSL::X509::Name.parse(subject)
    cert.public_key = public_key
    cert.not_before = Time.now - 60
    cert.not_after = Time.now + 3600
    cert
  end

  def extension_factory(subject_cert, issuer_cert)
    factory = OpenSSL::X509::ExtensionFactory.new
    factory.subject_certificate = subject_cert
    factory.issuer_certificate = issuer_cert
    factory
  end

  # Answers CheckBulkPermissions so a completed call proves the whole path --
  # handshake, HTTP/2, gRPC framing -- and not just a socket that opened.
  class TlsService < Authzed::Api::V1::PermissionsService::Service
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
end

# Caller-supplied TLS trust material, proven against a real TLS handshake.
#
# Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
# lets .new_system_tls delegate to gRPC's default trust source *because* a
# caller can supply their own material when that default is not enough -- and
# it names the hazard that makes the escape hatch necessary: gRPC's C-core
# compiles in its own roots.pem, so a CA an operator installed in the host's
# trust store is not honoured. Until .new_custom_tls existed, a SpiceDB behind
# a private or corporate CA was simply unreachable, and that justification was
# not true.
#
# Every connection spec below runs a real GRPC::RpcServer over TLS presenting a
# certificate signed by the throwaway CA above, and drives a real client
# against it. The pairing is what makes each assertion mean something: same
# server, same client code, differing only in whether the material was
# supplied. A spec that only asserted the failure could not tell a rejected
# certificate from an unreachable port; one that only asserted the success
# could not tell a verified chain from a disabled one.
RSpec.describe 'SpiceDB::Client.new_custom_tls' do
  def start_tls_server(require_client_auth: false)
    credentials = GRPC::Core::ServerCredentials.new(
      require_client_auth ? TestPki.ca[:cert].to_pem : nil,
      [{ private_key: TestPki.server[:key].to_pem, cert_chain: TestPki.server[:cert].to_pem }],
      require_client_auth
    )
    server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    port = server.add_http2_port('localhost:0', credentials)
    server.handle(TestPki::TlsService.new)
    Thread.new { server.run }
    server.wait_till_running(5)
    [server, port]
  end

  let(:ca_pem) { TestPki.ca[:cert].to_pem }
  let(:client_cert_pem) { TestPki.client[:cert].to_pem }
  let(:client_key_pem) { TestPki.client[:key].to_pem }
  let(:rel) { SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'jimmy') }

  # A real check, not an empty one: check_permissions short-circuits to [] with
  # no relationships and never opens a connection, so an empty call would pass
  # against a server it never reached.
  def check(client)
    client.check_permissions(SpiceDB::Consistency.full, 'view', rel)
  end

  it 'completes a handshake the default roots reject' do
    server, port = start_tls_server
    begin
      SpiceDB::Client.new_custom_tls("localhost:#{port}", 'testtoken', ca_cert: ca_pem,
                                                                       default_timeout: 10) do |client|
        expect(check(client).map(&:has_permission?)).to eq([true])
      end

      # Same server, same call, no CA: the roots compiled into gRPC have never
      # heard of the CA above, which is exactly the position an operator behind
      # a private CA is in.
      SpiceDB::Client.new_system_tls("localhost:#{port}", 'testtoken', default_timeout: 5) do |client|
        expect { check(client) }.to raise_error(SpiceDB::Error)
      end
    ensure
      server.stop
    end
  end

  it 'presents a client identity to a server requiring mutual TLS' do
    server, port = start_tls_server(require_client_auth: true)
    begin
      SpiceDB::Client.new_custom_tls("localhost:#{port}", 'testtoken',
                                     ca_cert: ca_pem, client_cert: client_cert_pem,
                                     client_key: client_key_pem, default_timeout: 10) do |client|
        expect(check(client).map(&:has_permission?)).to eq([true])
      end

      # Identical ca_cert, so this can only fail for the missing client
      # identity -- the server refuses any connection that does not present a
      # certificate under its CA.
      SpiceDB::Client.new_custom_tls("localhost:#{port}", 'testtoken', ca_cert: ca_pem,
                                                                       default_timeout: 5) do |client|
        expect { check(client) }.to raise_error(SpiceDB::Error)
      end
    ensure
      server.stop
    end
  end

  it 'returns the client directly when no block is given' do
    server, port = start_tls_server
    begin
      client = SpiceDB::Client.new_custom_tls("localhost:#{port}", 'testtoken', ca_cert: ca_pem,
                                                                                default_timeout: 10)
      expect(client).to be_a(SpiceDB::Client)
      expect(check(client).map(&:has_permission?)).to eq([true])
      client.close
    ensure
      server.stop
    end
  end
end

RSpec.describe 'SpiceDB::Client.new_custom_tls argument validation' do
  it 'refuses to be a disguised new_system_tls' do
    # A constructor named for custom trust material that silently used the
    # roots compiled into gRPC instead would be a quiet way to believe a
    # private CA was configured when it was not.
    expect do
      SpiceDB::Client.new_custom_tls('spicedb.example.com:443', 'testtoken')
    end.to raise_error(ArgumentError, /new_system_tls/)
  end

  it 'refuses a client certificate without its key' do
    expect do
      SpiceDB::Client.new_custom_tls('spicedb.example.com:443', 'testtoken', client_cert: 'pem')
    end.to raise_error(ArgumentError, /client_key/)
  end

  it 'refuses a client key without its certificate' do
    expect do
      SpiceDB::Client.new_custom_tls('spicedb.example.com:443', 'testtoken', client_key: 'pem')
    end.to raise_error(ArgumentError, /client_cert/)
  end

  # Root DESIGN.md, "RULE: Credentials over insecure transport require an
  # explicit opt-in". There is deliberately no plaintext route that accepts
  # trust material: .new_plaintext takes none, and the private #initialize
  # refuses the combination outright rather than discarding the material and
  # sending the bearer token in cleartext behind a call site that reads as
  # though TLS were configured.
  it 'refuses trust material on the plaintext path, rather than ignoring it' do
    expect do
      SpiceDB::Client.new(endpoint: 'localhost:50051', token: 'testtoken', insecure: true,
                          ca_cert: 'pem')
    end.to raise_error(ArgumentError, /insecure: true and TLS material/)
  end

  it 'keeps the non-loopback refusal ahead of the TLS one' do
    # Trust material must not become a second construction path that skips the
    # credential guard -- and the credential-leak message, not the TLS one, is
    # what a caller sees.
    expect do
      SpiceDB::Client.new(endpoint: 'evil.example.com:443', token: 'testtoken', insecure: true,
                          ca_cert: 'pem')
    end.to raise_error(SpiceDB::InvalidArgumentError, /allow_insecure_remote_credentials/)
  end

  it 'does not expose the block-form helper as a public class method' do
    # yield_or_return is private in SpiceDB::Connecting, so extending Client
    # must not widen the public surface.
    expect(SpiceDB::Client).not_to respond_to(:yield_or_return)
  end
end
