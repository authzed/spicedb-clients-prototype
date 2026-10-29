# frozen_string_literal: true

require 'socket'
require 'tmpdir'
require_relative '../spec_helper'

# Every endpoint form a plaintext client accepts reaches a real SpiceDB -- see
# root DESIGN.md, "Accept the endpoint forms deployments use, or reject them at
# construction": a target the client accepts must work.
#
# Beyond host:port, grpc-ruby's C-core understands two gRPC target forms, so
# this client accepts them:
#
# - dns:///host:port uses gRPC's DNS resolver, which spreads calls across every
#   address the name resolves to (client-side load balancing over a headless
#   Kubernetes service, for example). The loopback check runs on the host.
# - unix:/path dials a Unix domain socket. A socket never leaves the host, so it
#   needs no remote-plaintext opt-in.
#
# Any other gRPC target form (ipv4:, xds:, a dns target naming its own DNS
# server) is refused at construction rather than passed through.
RSpec.describe 'Endpoint forms' do
  host, port = SPICEDB_ENDPOINT.match(/\A(.*):(\d+)\z/).captures

  def expect_working(endpoint)
    SpiceDB::Client.new_plaintext(endpoint, SPICEDB_TOKEN) do |c|
      expect(c.read_schema.schema).to include('definition document')
    end
  end

  it 'reaches SpiceDB through host:port' do
    expect_working("#{host}:#{port}")
  end

  it 'reaches SpiceDB through http://host:port' do
    expect_working("http://#{host}:#{port}")
  end

  it 'reaches SpiceDB through dns:///host:port' do
    expect_working("dns:///#{host}:#{port}")
  end

  it 'reaches SpiceDB through a unix: socket' do
    # SpiceDB itself listens on TCP here, so a small relay bridges a socket
    # file to it; the client's side of the connection is a real Unix socket.
    Dir.mktmpdir do |dir|
      path = File.join(dir, 'spicedb.sock')
      server = UNIXServer.new(path)
      relay = Thread.new do
        loop do
          local = server.accept
          remote = TCPSocket.new(host, port.to_i)
          [[local, remote], [remote, local]].each do |from, to|
            Thread.new do
              IO.copy_stream(from, to)
            rescue IOError, SystemCallError
              nil
            ensure
              to.close_write rescue nil # rubocop:disable Style/RescueModifier
            end
          end
        end
      rescue IOError, SystemCallError
        nil
      end

      begin
        expect_working("unix:#{path}")
      ensure
        server.close
        relay.join(5)
      end
    end
  end

  it 'refuses a dns target to a remote host without the opt-in', :no_spicedb do
    expect { SpiceDB::Client.new_plaintext('dns:///spicedb.example.com:50051', SPICEDB_TOKEN) }
      .to raise_error(SpiceDB::InvalidArgumentError, /RemoteCredentials::ALLOW_PLAINTEXT/)
  end

  %w[ipv4:127.0.0.1:50051 xds:///spicedb:50051 dns://8.8.8.8/localhost:50051].each do |endpoint|
    it "refuses #{endpoint} at construction, naming the accepted forms", :no_spicedb do
      expect { SpiceDB::Client.new_plaintext(endpoint, SPICEDB_TOKEN) }
        .to raise_error(SpiceDB::InvalidArgumentError, %r{expected host:port.*dns:///host:port})
    end
  end
end
