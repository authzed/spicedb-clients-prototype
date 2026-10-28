# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

# Transport-level adoption behavior against a real in-process gRPC server:
# a client over a caller-owned channel (from_channel), per-call metadata and
# response header/trailer capture, and imports reporting the server's abort
# status rather than a transport send failure.
module AdoptionTransport
  SEEN = Queue.new

  class SchemaService < Authzed::Api::V1::SchemaService::Service
    def read_schema(_request, call)
      SEEN << { authorization: call.metadata['authorization'], trace: call.metadata['x-trace-id'] }
      call.merge_metadata_to_send('x-spicedb-version' => 'v9.9.9')
      call.output_metadata['x-trailer'] = 'done'
      Authzed::Api::V1::ReadSchemaResponse.new(schema_text: 'definition user {}',
                                               read_at: Authzed::Api::V1::ZedToken.new(token: 'rev'))
    end
  end

  class PermissionsService < Authzed::Api::V1::PermissionsService::Service
    # Reads one message and aborts the stream, as SpiceDB does on a duplicate.
    def import_bulk_relationships(call)
      call.each_remote_read do |_request| # rubocop:disable Lint/UnreachableLoop
        SEEN << :import_message
        raise GRPC::AlreadyExists, 'duplicate relationship document:x#viewer@user:y'
      end
    end
  end
end

RSpec.describe 'SpiceDB::Client transport escape hatches' do
  before(:all) do
    @server = GRPC::RpcServer.new(pool_size: 4, pool_keep_alive: 0.1)
    @port = @server.add_http2_port('localhost:0', :this_port_is_insecure)
    @server.handle(AdoptionTransport::SchemaService.new)
    @server.handle(AdoptionTransport::PermissionsService.new)
    @server_thread = Thread.new { @server.run }
    @server.wait_till_running(5)
  end

  after(:all) do
    @server.stop
    @server_thread&.join(5)
  end

  before { AdoptionTransport::SEEN.clear }

  it 'builds a client over a caller-owned channel and never closes it' do
    channel = GRPC::Core::Channel.new("localhost:#{@port}", {}, :this_channel_is_insecure)
    client = SpiceDB::Client.from_channel(channel, interceptors: [SpiceDBProto::BearerTokenInterceptor.new('channel-token')],
                                                   default_deadline: SpiceDB::DefaultDeadline::DISABLED)
    expect(client.read_schema.schema).to eq('definition user {}')
    expect(AdoptionTransport::SEEN.pop[:authorization]).to eq('Bearer channel-token')

    client.close
    expect { channel.connectivity_state }.not_to raise_error
    second = SpiceDB::Client.from_channel(channel, interceptors: [SpiceDBProto::BearerTokenInterceptor.new('channel-token')])
    expect(second.read_schema.revision).to eq(SpiceDB::ZedToken.new('rev'))
    channel.close
  end

  it 'rejects something that is not a channel' do
    expect { SpiceDB::Client.from_channel('localhost:1') }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'sends per-call metadata and captures response headers and trailers' do
    SpiceDB::Client.new_plaintext("localhost:#{@port}", 'token') do |client|
      capture = SpiceDB::ResponseMetadata.new
      client.read_schema(call_options: SpiceDB::CallOptions.new(metadata: { 'x-trace-id' => 'abc' }, response_metadata: capture))
      expect(AdoptionTransport::SEEN.pop[:trace]).to eq('abc')
      expect(capture.header['x-spicedb-version']).to eq('v9.9.9')
      expect(capture.trailer['x-trailer']).to eq('done')
    end
  end

  it "reports the server's abort status, not the transport's send failure, when an import is aborted" do
    relationships = Array.new(20_000) do |index|
      SpiceDB::Relationship.from_tuple("document:doc#{index}#viewer@user:alice")
    end
    SpiceDB::Client.new_plaintext("localhost:#{@port}", 'token') do |client|
      expect { client.import_relationships(relationships, batch_size: 10) }.to raise_error(SpiceDB::AlreadyExistsError) do |error|
        expect(error.server_message).to eq('duplicate relationship document:x#viewer@user:y')
        expect(error.code).to eq(GRPC::Core::StatusCodes::ALREADY_EXISTS)
      end
    end
    expect(AdoptionTransport::SEEN.pop).to eq(:import_message)
  end
end
