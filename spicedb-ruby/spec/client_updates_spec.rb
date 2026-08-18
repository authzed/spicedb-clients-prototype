# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'
require 'grpc'

RSpec.describe 'SpiceDB::Client#updates error mapping' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  # Simulates a mid-stream gRPC failure (e.g. the watched revision has been
  # garbage collected) by making the underlying proto stream's #each raise.
  def stub_watch_failure(bad_status)
    watch_response = double('watch_response')
    allow(watch_response).to receive(:each).and_raise(bad_status)

    watch_service = double('watch_service')
    allow(watch_service).to receive(:watch).and_return(watch_response)

    proto_client = double('proto_client', watch: watch_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  it 'maps a NOT_FOUND error raised mid-stream to SpiceDB::NotFoundError, not the raw gRPC error' do
    bad_status = GRPC::BadStatus.new(5, 'revision has been garbage collected')
    stub_watch_failure(bad_status)

    expect do
      client.updates(['document']).each { |_update| nil }
    end.to raise_error(SpiceDB::NotFoundError, 'revision has been garbage collected')
  end

  it 'does not leave the raw GRPC::BadStatus unrescued' do
    bad_status = GRPC::BadStatus.new(5, 'revision has been garbage collected')
    stub_watch_failure(bad_status)

    expect { client.updates(['document']).first }.to raise_error(SpiceDB::NotFoundError)
  end
end
