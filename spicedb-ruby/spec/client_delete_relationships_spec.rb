# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# Mirrors spicedb-go's client/relationships.go DeleteOption/WithDeleteMustMatch/
# WithDeleteMustNotMatch/WithDeleteLimit: delete_relationships gains optional
# preconditions (must_match/must_not_match) and a per-call limit override, on
# top of the existing filter-only, auto-paging behavior.
RSpec.describe 'SpiceDB::Client#delete_relationships preconditions and limit' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:filter) { SpiceDB::Filter.new(resource_type: 'document') }

  # Stubs @proto_client.permissions.delete_relationships to return a
  # single-page COMPLETE response, and captures each request sent so specs
  # can assert on its contents.
  def stub_delete_relationships(requests, revision: 'rev1')
    response = Authzed::Api::V1::DeleteRelationshipsResponse.new(
      deleted_at: Authzed::Api::V1::ZedToken.new(token: revision),
      deletion_progress: :DELETION_PROGRESS_COMPLETE
    )

    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:delete_relationships) do |req|
      requests << req
      response
    end

    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  describe 'default behavior (no kwargs)' do
    it 'sends no preconditions and the default page size, unchanged from before' do
      requests = []
      stub_delete_relationships(requests)

      revision = client.delete_relationships(filter)

      expect(revision).to eq('rev1')
      expect(requests.length).to eq(1)
      req = requests.first
      expect(req.optional_preconditions).to eq([])
      expect(req.optional_limit).to eq(SpiceDB::Client::DEFAULT_DELETE_PAGE_SIZE)
      expect(req.optional_allow_partial_deletions).to be(true)
    end
  end

  # Regression tests for the offboarding hazard this finding describes:
  # filter_to_proto used to build optional_subject_filter only inside
  # `if filter.subject_type`, so a filter with subject_id but no
  # subject_type produced a proto RelationshipFilter with NO subject
  # constraint at all -- delete_relationships called with that filter would
  # delete every relationship on every document, not just the intended
  # subject's. It must now raise before any RPC is attempted, instead of
  # silently widening.
  describe 'filter with subject_id but no subject_type' do
    it 'raises InvalidArgumentError naming subject_id and subject_type, and sends no request' do
      requests = []
      stub_delete_relationships(requests)
      bad_filter = SpiceDB::Filter.new(resource_type: 'document', subject_id: 'alice')

      expect { client.delete_relationships(bad_filter) }.to raise_error(SpiceDB::InvalidArgumentError) do |e|
        expect(e.message).to include('subject_id')
        expect(e.message).to include('subject_type')
      end
      expect(requests).to be_empty
    end
  end

  describe 'filter with subject_relation but no subject_type' do
    it 'raises InvalidArgumentError naming subject_relation and subject_type' do
      requests = []
      stub_delete_relationships(requests)
      bad_filter = SpiceDB::Filter.new(resource_type: 'document', subject_relation: 'member')

      expect { client.delete_relationships(bad_filter) }.to raise_error(SpiceDB::InvalidArgumentError) do |e|
        expect(e.message).to include('subject_relation')
        expect(e.message).to include('subject_type')
      end
      expect(requests).to be_empty
    end
  end

  describe 'must_match: filter with subject_id but no subject_type' do
    it 'raises InvalidArgumentError and sends no request' do
      requests = []
      stub_delete_relationships(requests)
      bad_guard = SpiceDB::Filter.new(resource_type: 'document', subject_id: 'alice')

      expect do
        client.delete_relationships(filter, must_match: [bad_guard])
      end.to raise_error(SpiceDB::InvalidArgumentError)
      expect(requests).to be_empty
    end
  end

  describe 'subject_type alone (no subject_id)' do
    it 'does not raise, and sends a subject filter with an empty optional_subject_id' do
      requests = []
      stub_delete_relationships(requests)
      type_only_filter = SpiceDB::Filter.new(resource_type: 'document', subject_type: 'user')

      client.delete_relationships(type_only_filter)

      req = requests.first
      expect(req.relationship_filter.optional_subject_filter.subject_type).to eq('user')
      expect(req.relationship_filter.optional_subject_filter.optional_subject_id).to eq('')
    end
  end

  describe 'subject_type and subject_id together' do
    it 'does not raise, and sends the full subject filter' do
      requests = []
      stub_delete_relationships(requests)
      full_filter = SpiceDB::Filter.new(resource_type: 'document', subject_type: 'user', subject_id: 'alice')

      client.delete_relationships(full_filter)

      req = requests.first
      expect(req.relationship_filter.optional_subject_filter.subject_type).to eq('user')
      expect(req.relationship_filter.optional_subject_filter.optional_subject_id).to eq('alice')
    end
  end

  describe 'must_match:' do
    it 'adds a MUST_MATCH precondition built from the filter' do
      requests = []
      stub_delete_relationships(requests)
      guard = SpiceDB::Filter.new(resource_type: 'document').with_relation('owner')

      client.delete_relationships(filter, must_match: [guard])

      req = requests.first
      expect(req.optional_preconditions.length).to eq(1)
      precondition = req.optional_preconditions.first
      expect(precondition.operation).to eq(:OPERATION_MUST_MATCH)
      expect(precondition.filter.resource_type).to eq('document')
      expect(precondition.filter.optional_relation).to eq('owner')
    end
  end

  describe 'must_not_match:' do
    it 'adds a MUST_NOT_MATCH precondition built from the filter' do
      requests = []
      stub_delete_relationships(requests)
      guard = SpiceDB::Filter.new(resource_type: 'document').with_relation('banned')

      client.delete_relationships(filter, must_not_match: [guard])

      req = requests.first
      expect(req.optional_preconditions.length).to eq(1)
      precondition = req.optional_preconditions.first
      expect(precondition.operation).to eq(:OPERATION_MUST_NOT_MATCH)
      expect(precondition.filter.resource_type).to eq('document')
      expect(precondition.filter.optional_relation).to eq('banned')
    end
  end

  describe 'combined must_match: and must_not_match:' do
    it 'sends both preconditions in order' do
      requests = []
      stub_delete_relationships(requests)
      must = SpiceDB::Filter.new(resource_type: 'document').with_relation('owner')
      must_not = SpiceDB::Filter.new(resource_type: 'document').with_relation('banned')

      client.delete_relationships(filter, must_match: [must], must_not_match: [must_not])

      ops = requests.first.optional_preconditions.map(&:operation)
      expect(ops).to eq(%i[OPERATION_MUST_MATCH OPERATION_MUST_NOT_MATCH])
    end
  end

  describe 'limit:' do
    it 'overrides the default per-page limit' do
      requests = []
      stub_delete_relationships(requests)

      client.delete_relationships(filter, limit: 1)

      expect(requests.first.optional_limit).to eq(1)
    end

    it 'falls back to DEFAULT_DELETE_PAGE_SIZE when not given' do
      requests = []
      stub_delete_relationships(requests)

      client.delete_relationships(filter)

      expect(requests.first.optional_limit).to eq(SpiceDB::Client::DEFAULT_DELETE_PAGE_SIZE)
    end
  end

  describe 'multi-page delete with preconditions' do
    it 're-sends the same preconditions on every page (server re-evaluates per page)' do
      partial_response = Authzed::Api::V1::DeleteRelationshipsResponse.new(
        deleted_at: Authzed::Api::V1::ZedToken.new(token: 'rev-partial'),
        deletion_progress: :DELETION_PROGRESS_PARTIAL
      )
      complete_response = Authzed::Api::V1::DeleteRelationshipsResponse.new(
        deleted_at: Authzed::Api::V1::ZedToken.new(token: 'rev-complete'),
        deletion_progress: :DELETION_PROGRESS_COMPLETE
      )

      requests = []
      permissions_service = double('permissions_service')
      responses = [partial_response, complete_response]
      allow(permissions_service).to receive(:delete_relationships) do |req|
        requests << req
        responses.shift
      end
      proto_client = double('proto_client', permissions: permissions_service)
      client.instance_variable_set(:@proto_client, proto_client)

      guard = SpiceDB::Filter.new(resource_type: 'document').with_relation('owner')
      revision = client.delete_relationships(filter, must_match: [guard], limit: 1)

      expect(revision).to eq('rev-complete')
      expect(requests.length).to eq(2)
      expect(requests[0].optional_preconditions.length).to eq(1)
      expect(requests[1].optional_preconditions.length).to eq(1)
      expect(requests[0].optional_limit).to eq(1)
      expect(requests[1].optional_limit).to eq(1)
    end
  end
end
