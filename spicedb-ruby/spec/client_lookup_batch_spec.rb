# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# lookup_resources' response_batch_size: forwards the proto's new
# optional_response_batch_size field and transparently unwraps a batched
# LookupResourcesResponse (LookupResourcesBatch) into one LookupResource per
# result, sharing the batch's single cursor and the response's looked_up_at.
RSpec.describe 'LookupResources response batching' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:arguments) { { resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full } }

  def stub_lookup_resources(&respond)
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:lookup_resources) do |request, **_kwargs|
      respond.call(request)
    end
    client.instance_variable_set(:@proto_client, double('proto_client', permissions: permissions_service))
  end

  it 'unwraps a batched response into one LookupResource per result, sharing the batch cursor and looked_up_at' do
    stub_lookup_resources do |_request|
      FixtureOperation.new([
                             Authzed::Api::V1::LookupResourcesResponse.new(
                               looked_up_at: Authzed::Api::V1::ZedToken.new(token: 'zed-lookup-batch'),
                               batched: Authzed::Api::V1::LookupResourcesBatch.new(
                                 results: [
                                   Authzed::Api::V1::LookupResourcesResult.new(resource_object_id: 'doc1',
                                                                               permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
                                   Authzed::Api::V1::LookupResourcesResult.new(
                                     resource_object_id: 'doc2', permissionship: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                                     partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['ip_address'])
                                   )
                                 ],
                                 after_cursor: Authzed::Api::V1::Cursor.new(token: 'batch-cursor')
                               )
                             )
                           ])
    end

    results = client.lookup_resources(**arguments, response_batch_size: 1000).to_a

    expect(results).to eq(
      [
        SpiceDB::LookupResource.new(resource_id: 'doc1', permissionship: :has_permission, partial_caveat: nil,
                                    looked_up_at: SpiceDB::ZedToken.new('zed-lookup-batch'), cursor: 'batch-cursor'),
        SpiceDB::LookupResource.new(resource_id: 'doc2', permissionship: :conditional_permission,
                                    partial_caveat: SpiceDB::PartialCaveatInfo.new(missing_required_context: ['ip_address']),
                                    looked_up_at: SpiceDB::ZedToken.new('zed-lookup-batch'), cursor: 'batch-cursor')
      ]
    )
  end

  it 'fails a batched response with no results with INTERNAL' do
    stub_lookup_resources do |_request|
      FixtureOperation.new([Authzed::Api::V1::LookupResourcesResponse.new(batched: Authzed::Api::V1::LookupResourcesBatch.new(results: []))])
    end

    expect { client.lookup_resources(**arguments, response_batch_size: 1000).to_a }.to raise_error(SpiceDB::Error,
                                                                                                   /response contains no results/) do |e|
      expect(e.code).to eq(GRPC::Core::StatusCodes::INTERNAL)
    end
  end

  it 'sends optional_response_batch_size on the request' do
    sent = nil
    stub_lookup_resources do |request|
      sent = request
      FixtureOperation.new([])
    end

    client.lookup_resources(**arguments, response_batch_size: 1000).to_a

    expect(sent.optional_response_batch_size).to eq(1000)
  end

  it 'defaults optional_response_batch_size to 0 (one resource per message)' do
    sent = nil
    stub_lookup_resources do |request|
      sent = request
      FixtureOperation.new([])
    end

    client.lookup_resources(**arguments).to_a

    expect(sent.optional_response_batch_size).to eq(0)
  end

  [-1, 1.5, '1000'].each do |invalid|
    it "rejects response_batch_size: #{invalid.inspect} before transport" do
      expect { client.lookup_resources(**arguments, response_batch_size: invalid) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end
end
