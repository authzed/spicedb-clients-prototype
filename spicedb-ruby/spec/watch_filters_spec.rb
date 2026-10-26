# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'

RSpec.describe 'watch relationship filters' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'key') }

  after { client.close }

  it 'rejects conflicting scopes before invoking transport' do
    expect(client.proto_client.watch).not_to receive(:watch)
    expect do
      client.watch(object_types: ['document'], filters: [SpiceDB::Filter.new(resource_type: 'document')])
    end.to raise_error(SpiceDB::InvalidArgumentError, /mutually exclusive/)
  end

  %i[filters empty object_types].each do |scope|
    it "snapshots #{scope} and sends full OR filters" do
      filters = if scope == :filters
                  [SpiceDB::Filter.new(resource_type: 'document', resource_id: 'one', relation: 'viewer',
                                       subject: SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: 'alice',
                                                                           relation: SpiceDB::SubjectRelationFilter.direct)),
                   SpiceDB::Filter.new(resource_type: 'folder', resource_id_prefix: 'team/', relation: 'editor',
                                       subject: SpiceDB::SubjectFilter.new(subject_type: 'group', subject_id: 'eng',
                                                                           relation: SpiceDB::SubjectRelationFilter.named('member')))]
                else
                  []
                end
      object_types = scope == :object_types ? ['document'] : []
      captured = nil
      allow(client.proto_client.watch).to receive(:watch) do |request, **_kwargs|
        captured = request
        FixtureOperation.new([])
      end
      stream = client.watch(filters: filters, object_types: object_types)
      filters.clear
      object_types.clear
      expect(stream.to_a).to eq([])
      expected = if scope == :filters
                   [Authzed::Api::V1::RelationshipFilter.new(
                     resource_type: 'document', optional_resource_id: 'one', optional_relation: 'viewer',
                     optional_subject_filter: Authzed::Api::V1::SubjectFilter.new(
                       subject_type: 'user', optional_subject_id: 'alice',
                       optional_relation: Authzed::Api::V1::SubjectFilter::RelationFilter.new(relation: '')
                     )
                   ), Authzed::Api::V1::RelationshipFilter.new(
                     resource_type: 'folder', optional_resource_id_prefix: 'team/', optional_relation: 'editor',
                     optional_subject_filter: Authzed::Api::V1::SubjectFilter.new(
                       subject_type: 'group', optional_subject_id: 'eng',
                       optional_relation: Authzed::Api::V1::SubjectFilter::RelationFilter.new(relation: 'member')
                     )
                   )]
                 else
                   []
                 end
      expect(captured.optional_relationship_filters.to_a).to eq(expected)
      expect(captured.optional_object_types.to_a).to eq(scope == :object_types ? ['document'] : [])
    end
  end
end
