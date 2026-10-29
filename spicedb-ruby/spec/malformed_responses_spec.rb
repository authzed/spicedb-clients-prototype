# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Root DESIGN.md, "Expose native errors without losing server diagnostics": a
# response the client cannot interpret fails with INTERNAL, which no default
# retry policy retries. It is never skipped and never filled with empty
# values.
RSpec.describe 'malformed responses' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'token') }
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:readme) { SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme') }
  let(:filter) { SpiceDB::Filter.new(resource_type: 'document') }
  let(:viewer) { SpiceDB::ProtoConv.relationship_to_proto(SpiceDB::Relationship.new(resource: readme, relation: 'viewer', subject: alice)) }
  let(:cursor) { Authzed::Api::V1::Cursor.new(token: 'c1') }

  # Installs a permissions service whose +method+ streams +responses+, and
  # returns the call count.
  def serve(method, responses, service_name: :permissions)
    calls = [0]
    service = double(service_name.to_s)
    allow(service).to receive(method) do |*|
      calls[0] += 1
      FixtureOperation.new(responses)
    end
    client.instance_variable_set(:@proto_client, double('proto', service_name => service))
    calls
  end

  def expect_internal(pattern, calls, &block)
    expect(&block).to raise_error(SpiceDB::Error, pattern) { |e| expect(e.code).to eq(GRPC::Core::StatusCodes::INTERNAL) }
    expect(calls[0]).to eq(1), 'a malformed response is not retried'
  end

  def read_with(relationship)
    calls = serve(:read_relationships, [
                    Authzed::Api::V1::ReadRelationshipsResponse.new(relationship: viewer, after_result_cursor: cursor),
                    Authzed::Api::V1::ReadRelationshipsResponse.new(relationship: relationship, after_result_cursor: cursor)
                  ])
    seen = []
    [calls, seen, -> { client.read_relationships(filter, consistency: SpiceDB::Consistency.full).each { |item| seen << item } }]
  end

  it 'fails a read response with no relationship, after yielding the good one before it' do
    calls, seen, run = read_with(nil)
    expect_internal(/malformed response: relationship is missing/, calls) { run.call }
    expect(seen.length).to eq(1)
  end

  it 'fails a read relationship with no subject' do
    calls, _, run = read_with(viewer.dup.tap { |rel| rel.subject = nil })
    expect_internal(/relationship\.subject is missing/, calls) { run.call }
  end

  it 'fails a read relationship with no resource' do
    calls, _, run = read_with(viewer.dup.tap { |rel| rel.resource = nil })
    expect_internal(/relationship\.resource is missing/, calls) { run.call }
  end

  it 'fails a read relationship whose subject has no object' do
    broken = viewer.dup
    broken.subject = Authzed::Api::V1::SubjectReference.new(object: nil)
    calls, _, run = read_with(broken)
    expect_internal(/relationship\.subject\.object is missing/, calls) { run.call }
  end

  it 'fails a read relationship with an empty object type as INTERNAL, not as the caller\'s INVALID_ARGUMENT' do
    broken = viewer.dup
    broken.resource = Authzed::Api::V1::ObjectReference.new(object_type: '', object_id: 'x')
    calls, _, run = read_with(broken)
    expect_internal(/relationship\.resource/, calls) { run.call }
  end

  it 'fails a lookup_resources response with no resource ID' do
    calls = serve(:lookup_resources, [Authzed::Api::V1::LookupResourcesResponse.new(after_result_cursor: cursor)])
    expect_internal(/resource_object_id is missing/, calls) do
      client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full).to_a
    end
  end

  it 'fails a lookup_subjects response with no subject' do
    calls = serve(:lookup_subjects, [Authzed::Api::V1::LookupSubjectsResponse.new])
    expect_internal(/lookup_subjects: subject is missing/, calls) do
      client.lookup_subjects(resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a
    end
  end

  it 'fails an export batch holding a relationship with no subject' do
    broken = viewer.dup.tap { |rel| rel.subject = nil }
    calls = serve(:export_bulk_relationships, [Authzed::Api::V1::ExportBulkRelationshipsResponse.new(relationships: [viewer, broken],
                                                                                                     after_result_cursor: cursor)])
    expect_internal(/relationship\.subject is missing/, calls) { client.export_relationships(consistency: SpiceDB::Consistency.full).to_a }
  end

  it 'fails a watch update with no relationship' do
    update = Authzed::Api::V1::RelationshipUpdate.new(operation: :OPERATION_TOUCH)
    calls = serve(:watch, [Authzed::Api::V1::WatchResponse.new(updates: [update])], service_name: :watch)
    expect_internal(/watch update relationship is missing/, calls) { client.watch { |_event| nil } }
  end

  it 'fails a paged read whose cursor does not advance with INTERNAL' do
    calls = serve(:lookup_resources, Array.new(2) do |i|
      Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: i.to_s, after_result_cursor: cursor)
    end)
    expect do
      client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full,
                              page_size: 2).to_a
    end.to raise_error(SpiceDB::Error, /cursor did not advance/) { |e| expect(e.code).to eq(GRPC::Core::StatusCodes::INTERNAL) }
    expect(calls[0]).to eq(2)
  end

  it 'reports use after close as FAILED_PRECONDITION, not UNKNOWN' do
    client.close
    expect { client.read_schema }.to raise_error(SpiceDB::FailedPreconditionError, /client is closed/)
  end
end
