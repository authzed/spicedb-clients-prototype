# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# A watch stream that dies cannot be correctly resumed unless the client
# surfaces changes_through (proto: "This token can be used in a subsequent
# WatchRequest to resume watching from this point"), and cannot survive an
# idle-timeout proxy unless the client can request
# WATCH_KIND_INCLUDE_CHECKPOINTS. These specs exercise both.
RSpec.describe 'SpiceDB::Client#updates resumability' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  def rel_proto(subject_id)
    Authzed::Api::V1::Relationship.new(
      resource: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: subject_id),
      relation: 'viewer',
      subject: Authzed::Api::V1::SubjectReference.new(
        object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice')
      )
    )
  end

  # Stubs the underlying proto watch stream to return `responses`, and
  # captures the WatchRequest actually built and sent -- so a spec can
  # assert on the wire, not just that the call succeeded.
  def stub_watch(responses)
    captured_request = nil
    watch_service = double('watch_service')
    allow(watch_service).to receive(:watch) do |req|
      captured_request = req
      FixtureOperation.new(responses)
    end

    proto_client = double('proto_client', watch: watch_service)
    client.instance_variable_set(:@proto_client, proto_client)
    -> { captured_request }
  end

  it 'exposes a usable resume token on a watch event' do
    stub_watch([Authzed::Api::V1::WatchResponse.new(
      changes_through: Authzed::Api::V1::ZedToken.new(token: 'resume-me')
    )])

    got = client.watch(object_types: ['document']).to_a

    expect(got.length).to eq(1)
    expect(got.first.changes_through).to eq(SpiceDB::ZedToken.new('resume-me'))
  end

  it 'requests relationship updates by default' do
    captured = stub_watch([])

    client.watch(object_types: ['document']).to_a

    expect(captured.call.optional_update_kinds).to eq([:WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES])
  end

  it 'reaches the wire with WATCH_KIND_INCLUDE_CHECKPOINTS when include_checkpoints is true' do
    captured = stub_watch([])

    client.watch(object_types: ['document'], content: [SpiceDB::WatchContent::RELATIONSHIPS, SpiceDB::WatchContent::CHECKPOINTS]).to_a

    kinds = captured.call.optional_update_kinds
    expect(kinds).to include(:WATCH_KIND_INCLUDE_CHECKPOINTS)
    # Requesting checkpoints must not silently drop relationship updates --
    # optional_update_kinds is empty-means-default, so a non-empty list is
    # the exact set requested.
    expect(kinds).to include(:WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES)
  end

  it 'makes a checkpoint event distinguishable from an event carrying updates' do
    stub_watch([
                 Authzed::Api::V1::WatchResponse.new(
                   changes_through: Authzed::Api::V1::ZedToken.new(token: 'checkpoint-rev'),
                   is_checkpoint: true
                 ),
                 Authzed::Api::V1::WatchResponse.new(
                   changes_through: Authzed::Api::V1::ZedToken.new(token: 'update-rev'),
                   updates: [
                     Authzed::Api::V1::RelationshipUpdate.new(
                       operation: :OPERATION_TOUCH,
                       relationship: rel_proto('doc1')
                     )
                   ]
                 )
               ])

    got = client.watch(object_types: ['document'], content: [SpiceDB::WatchContent::RELATIONSHIPS, SpiceDB::WatchContent::CHECKPOINTS]).to_a

    expect(got.length).to eq(2)

    expect(got[0].is_checkpoint).to be true
    expect(got[0].updates).to eq([])
    expect(got[0].changes_through).to eq(SpiceDB::ZedToken.new('checkpoint-rev'))

    expect(got[1].is_checkpoint).to be false
    expect(got[1].updates.length).to eq(1)
    expect(got[1].changes_through).to eq(SpiceDB::ZedToken.new('update-rev'))
  end
end
