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
    expect_internal(/client detected malformed response from server: relationship is missing/, calls) { run.call }
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
      Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: i.to_s, permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                                                    after_result_cursor: cursor)
    end)
    expect do
      client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full,
                              page_size: 2).to_a
    end.to raise_error(SpiceDB::Error, /cursor did not advance/) { |e| expect(e.code).to eq(GRPC::Core::StatusCodes::INTERNAL) }
    expect(calls[0]).to eq(2)
  end

  it 'says the client detected a malformed response, so it is not read as a status the server sent' do
    calls, _, run = read_with(nil)
    expect_internal(/\Aspicedb: client detected malformed response from server: relationship is missing\z/, calls) { run.call }
  end

  # Root DESIGN.md: an unspecified enum the server always sets, such as a
  # check's permissionship, is a malformed response, never :unspecified.
  describe 'an unspecified permissionship' do
    let(:check) { SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice) }

    def serve_unary(method, &respond)
      calls = [0]
      service = double('permissions')
      allow(service).to receive(method) do |request, **|
        calls[0] += 1
        respond.call(request)
      end
      client.instance_variable_set(:@proto_client, double('proto', permissions: service))
      calls
    end

    it 'fails a check with INTERNAL' do
      calls = serve_unary(:check_permission) { Authzed::Api::V1::CheckPermissionResponse.new(permissionship: :PERMISSIONSHIP_UNSPECIFIED) }
      expect_internal(/client detected malformed response from server: check: permissionship is unspecified/, calls) do
        client.check(check, consistency: SpiceDB::Consistency.full)
      end
    end

    [SpiceDB::BulkItemErrors::RAISED, SpiceDB::BulkItemErrors::RETURNED].each do |mode|
      it "fails a bulk-check item with INTERNAL, naming its index (#{mode})" do
        calls = serve_unary(:check_bulk_permissions) do |request|
          answers = %i[PERMISSIONSHIP_HAS_PERMISSION PERMISSIONSHIP_UNSPECIFIED]
          Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: request.items.each_with_index.map do |item, i|
            Authzed::Api::V1::CheckBulkPermissionsPair.new(
              request: item, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: answers[i])
            )
          end)
        end
        expect_internal(/client detected malformed response from server: check item 1: permissionship is unspecified/, calls) do
          client.check_bulk([check, check], consistency: SpiceDB::Consistency.full, item_errors: mode)
        end
      end
    end

    it 'fails a lookup_resources result with INTERNAL, after yielding the good one before it' do
      calls = serve(:lookup_resources, [
                      Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: 'a', permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                                                                    after_result_cursor: cursor),
                      Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: 'b', after_result_cursor: cursor)
                    ])
      seen = []
      expect_internal(/client detected malformed response from server: lookup_resources: permissionship is unspecified/, calls) do
        client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice,
                                consistency: SpiceDB::Consistency.full).each { |item| seen << item }
      end
      expect(seen.map(&:resource_id)).to eq(%w[a])
    end

    # Only the unset value is malformed: a permissionship newer than this
    # client maps to :unspecified, which never grants, and the call succeeds,
    # so upgrading SpiceDB cannot fail it.
    describe 'a permissionship newer than the client' do
      it 'maps a check to :unspecified without failing' do
        serve_unary(:check_permission) { Authzed::Api::V1::CheckPermissionResponse.new(permissionship: 99) }
        result = client.check(check, consistency: SpiceDB::Consistency.full)
        expect([result.permissionship, result.has_permission?]).to eq([:unspecified, false])
      end

      [SpiceDB::BulkItemErrors::RAISED, SpiceDB::BulkItemErrors::RETURNED].each do |mode|
        it "maps a bulk-check item to :unspecified without failing (#{mode})" do
          serve_unary(:check_bulk_permissions) do |request|
            answers = [:PERMISSIONSHIP_HAS_PERMISSION, 99]
            Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: request.items.each_with_index.map do |item, i|
              Authzed::Api::V1::CheckBulkPermissionsPair.new(
                request: item, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: answers[i])
              )
            end)
          end
          results = client.check_bulk([check, check], consistency: SpiceDB::Consistency.full, item_errors: mode)
          expect(results.map(&:permissionship)).to eq(%i[has_permission unspecified])
          expect(results.map(&:error)).to eq([nil, nil])
        end
      end

      it 'maps a lookup_resources result to :unspecified without failing' do
        serve(:lookup_resources, [Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: 'a', permissionship: 99,
                                                                                after_result_cursor: cursor)])
        found = client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice,
                                        consistency: SpiceDB::Consistency.full).to_a
        expect(found.map(&:permissionship)).to eq(%i[unspecified])
      end
    end
  end

  describe 'an unspecified lookup_subjects permissionship' do
    def subject(permissionship = nil) = Authzed::Api::V1::ResolvedSubject.new(subject_object_id: 'bob', permissionship: permissionship)

    def run(responses)
      calls = serve(:lookup_subjects, responses)
      seen = []
      expect_internal(/client detected malformed response from server: lookup_subjects: permissionship is unspecified/, calls) do
        client.lookup_subjects(resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full)
              .each { |item| seen << item }
      end
      seen
    end

    it 'fails on the resolved subject, after yielding the good one before it' do
      good = Authzed::Api::V1::LookupSubjectsResponse.new(subject: subject(:LOOKUP_PERMISSIONSHIP_HAS_PERMISSION))
      seen = run([good, Authzed::Api::V1::LookupSubjectsResponse.new(subject: subject)])
      expect(seen.map { |item| item.subject.subject_id }).to eq(%w[bob])
    end

    it 'fails on the deprecated top-level fields too' do
      run([Authzed::Api::V1::LookupSubjectsResponse.new(subject_object_id: 'bob')])
    end

    it 'leaves an excluded subject without one as sent' do
      calls = serve(:lookup_subjects, [Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: '*', permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        excluded_subjects: [subject]
      )])
      items = client.lookup_subjects(resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a
      expect(items.first.excluded_subjects.map(&:permissionship)).to eq(%i[unspecified])
      expect(calls[0]).to eq(1)
    end
  end

  it 'reports use after close as FAILED_PRECONDITION, not UNKNOWN' do
    client.close
    expect { client.read_schema }.to raise_error(SpiceDB::FailedPreconditionError, /client is closed/)
  end
end
