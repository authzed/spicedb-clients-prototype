# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# ExperimentalRoaringLookupResources lives in its own generated proto package
# (authzed.api.materialize.v0), wired to the idiomatic client via
# @proto_client.materialize -- distinct from the other experimental methods,
# which go through @proto_client.experimental. This pins that both the
# request is built correctly and the response maps to a native
# SpiceDB::RoaringLookupResourcesResult.
RSpec.describe 'SpiceDB::Client#experimental_roaring_lookup_resources' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def stub_roaring_lookup_resources(response)
    materialize_service = double('materialize_service')
    allow(materialize_service).to receive(:experimental_roaring_lookup_resources).and_return(response)
    proto_client = double('proto_client', materialize: materialize_service)
    client.instance_variable_set(:@proto_client, proto_client)
    materialize_service
  end

  it 'maps bitmap, cardinality, and at_revision from the proto response' do
    response = Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesResponse.new(
      bitmap: "\x01\x02\x03".b,
      cardinality: 3,
      at_revision: Authzed::Api::V1::ZedToken.new(token: 'zed-roaring-1')
    )
    stub_roaring_lookup_resources(response)

    result = client.experimental_roaring_lookup_resources(
      SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice'
    )

    expect(result).to eq(
      SpiceDB::RoaringLookupResourcesResult.new(
        bitmap: "\x01\x02\x03".b,
        cardinality: 3,
        at_revision: 'zed-roaring-1'
      )
    )
  end

  it 'sends resource type, permission, and subject on the request' do
    response = Authzed::Api::Materialize::V0::ExperimentalRoaringLookupResourcesResponse.new(
      bitmap: ''.b,
      cardinality: 0,
      at_revision: Authzed::Api::V1::ZedToken.new(token: '')
    )
    materialize_service = double('materialize_service')
    captured_request = nil
    allow(materialize_service).to receive(:experimental_roaring_lookup_resources) do |request, **_kwargs|
      captured_request = request
      response
    end
    proto_client = double('proto_client', materialize: materialize_service)
    client.instance_variable_set(:@proto_client, proto_client)

    client.experimental_roaring_lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice')

    expect(captured_request.resource_object_type).to eq('document')
    expect(captured_request.permission).to eq('view')
    expect(captured_request.subject.object.object_type).to eq('user')
    # Bracket access, not `.object_id` -- ObjectReference's "object_id" field
    # collides with Kernel#object_id, and the generated protobuf accessor
    # does not override it (a landmine pre-existing in this proto package,
    # not introduced by this method).
    expect(captured_request.subject.object['object_id']).to eq('alice')
  end
end
