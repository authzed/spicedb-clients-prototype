# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

# Mirrors spicedb-go's client/lookup_types.go + lookup.go: lookup_resources
# and lookup_subjects must yield native value objects carrying
# permissionship + partial caveat info, and — critically —
# lookup_subjects must surface excluded_subjects for a wildcard "*" match
# so callers can't accidentally over-grant access to an excluded subject.
RSpec.describe 'SpiceDB::Client lookup native results' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  # Stubs @proto_client.permissions.lookup_resources to return the given
  # array of proto LookupResourcesResponse (Array responds to #each, same
  # shape the real streaming call returns).
  def stub_lookup_resources(responses)
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:lookup_resources).and_return(responses)
    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  # Stubs @proto_client.permissions.lookup_subjects to return the given
  # array of proto LookupSubjectsResponse.
  def stub_lookup_subjects(responses)
    permissions_service = double('permissions_service')
    allow(permissions_service).to receive(:lookup_subjects).and_return(responses)
    proto_client = double('proto_client', permissions: permissions_service)
    client.instance_variable_set(:@proto_client, proto_client)
  end

  describe '#lookup_resources' do
    it 'yields a SpiceDB::LookupResource carrying a HAS_PERMISSION permissionship' do
      resp = Authzed::Api::V1::LookupResourcesResponse.new(
        resource_object_id: 'doc1',
        permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor1')
      )
      stub_lookup_resources([resp])

      results = client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice').to_a

      expect(results).to eq(
        [SpiceDB::LookupResource.new(resource_id: 'doc1', permissionship: :has_permission, partial_caveat: nil,
                                     looked_up_at: '')]
      )
    end

    it 'surfaces a conditional permissionship and its partial caveat info' do
      resp = Authzed::Api::V1::LookupResourcesResponse.new(
        resource_object_id: 'doc2',
        permissionship: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
        partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['ip_address']),
        after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor2')
      )
      stub_lookup_resources([resp])

      result = client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice').first

      expect(result.permissionship).to eq(:conditional_permission)
      expect(result.partial_caveat).to eq(SpiceDB::PartialCaveatInfo.new(missing_required_context: ['ip_address']))
    end

    it 'surfaces looked_up_at from the proto response' do
      resp = Authzed::Api::V1::LookupResourcesResponse.new(
        resource_object_id: 'doc3',
        permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        looked_up_at: Authzed::Api::V1::ZedToken.new(token: 'zed-lookup-1'),
        after_result_cursor: Authzed::Api::V1::Cursor.new(token: 'cursor3')
      )
      stub_lookup_resources([resp])

      result = client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice').first

      expect(result.looked_up_at).to eq('zed-lookup-1')
    end
  end

  describe '#lookup_subjects' do
    it 'surfaces excluded_subjects for a wildcard "*" match (the over-grant security fix)' do
      excluded = [
        Authzed::Api::V1::ResolvedSubject.new(subject_object_id: 'blocked1',
                                              permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        Authzed::Api::V1::ResolvedSubject.new(subject_object_id: 'blocked2',
                                              permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
      ]
      resp = Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: '*',
                                                       permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        excluded_subjects: excluded
      )
      stub_lookup_subjects([resp])

      result = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').first

      expect(result).to be_a(SpiceDB::LookupSubject)
      expect(result.subject.subject_id).to eq('*')
      expect(result.excluded_subjects.map(&:subject_id)).to eq(%w[blocked1 blocked2])
      expect(result.excluded_subjects).to all(be_a(SpiceDB::ResolvedSubject))
    end

    it 'falls back to the deprecated excluded_subject_ids field when excluded_subjects is empty' do
      resp = Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: '*',
                                                       permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        excluded_subject_ids: %w[legacy1 legacy2]
      )
      stub_lookup_subjects([resp])

      result = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').first

      expect(result.excluded_subjects).to eq(
        [
          SpiceDB::ResolvedSubject.new(subject_id: 'legacy1', permissionship: :unspecified, partial_caveat: nil),
          SpiceDB::ResolvedSubject.new(subject_id: 'legacy2', permissionship: :unspecified, partial_caveat: nil)
        ]
      )
    end

    it 'falls back to the deprecated top-level subject fields when `subject` is unset' do
      resp = Authzed::Api::V1::LookupSubjectsResponse.new(
        subject_object_id: 'alice',
        permissionship: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
        partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['region'])
      )
      stub_lookup_subjects([resp])

      result = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').first

      expect(result.subject).to eq(
        SpiceDB::ResolvedSubject.new(
          subject_id: 'alice',
          permissionship: :conditional_permission,
          partial_caveat: SpiceDB::PartialCaveatInfo.new(missing_required_context: ['region'])
        )
      )
      expect(result.excluded_subjects).to eq([])
    end

    it 'returns an empty excluded_subjects array when there is no wildcard exclusion' do
      resp = Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: 'bob',
                                                       permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
      )
      stub_lookup_subjects([resp])

      result = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').first

      expect(result.excluded_subjects).to eq([])
    end

    it 'surfaces looked_up_at from the proto response' do
      resp = Authzed::Api::V1::LookupSubjectsResponse.new(
        subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: 'bob',
                                                       permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
        looked_up_at: Authzed::Api::V1::ZedToken.new(token: 'zed-lookup-2')
      )
      stub_lookup_subjects([resp])

      result = client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').first

      expect(result.looked_up_at).to eq('zed-lookup-2')
    end
  end

  describe '#permissionship_from_proto' do
    it 'maps HAS_PERMISSION and CONDITIONAL_PERMISSION' do
      expect(client.send(:permissionship_from_proto,
                         :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)).to eq(:has_permission)
      expect(client.send(:permissionship_from_proto,
                         :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION)).to eq(:conditional_permission)
    end

    it 'maps unspecified/unrecognized/nil values to :unspecified' do
      expect(client.send(:permissionship_from_proto, :LOOKUP_PERMISSIONSHIP_UNSPECIFIED)).to eq(:unspecified)
      expect(client.send(:permissionship_from_proto, nil)).to eq(:unspecified)
    end
  end

  describe '#partial_caveat_from_proto' do
    it 'returns nil for a nil input' do
      expect(client.send(:partial_caveat_from_proto, nil)).to be_nil
    end

    it 'maps missing_required_context to a native PartialCaveatInfo' do
      proto = Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: %w[a b])
      expect(client.send(:partial_caveat_from_proto, proto)).to eq(
        SpiceDB::PartialCaveatInfo.new(missing_required_context: %w[a b])
      )
    end
  end

  describe '#resolved_subject_from_proto' do
    it 'returns a zero-value ResolvedSubject for a nil input' do
      result = client.send(:resolved_subject_from_proto, nil)
      expect(result).to eq(
        SpiceDB::ResolvedSubject.new(subject_id: nil, permissionship: :unspecified, partial_caveat: nil)
      )
    end
  end

  # Keyword arguments are the lookups' options container -- root DESIGN.md,
  # "RULE: Every RPC wrapper must have one place to add an option". These pin
  # that the container is actually wired to the request, not merely accepted
  # and dropped, and that an absent option sends no field at all rather than an
  # empty Struct.
  describe 'caveat context' do
    def capture_request(method, responses)
      captured = nil
      permissions_service = double('permissions_service')
      allow(permissions_service).to receive(method) do |req|
        captured = req
        responses
      end
      proto_client = double('proto_client', permissions: permissions_service)
      client.instance_variable_set(:@proto_client, proto_client)
      -> { captured }
    end

    it 'sends context supplied to lookup_resources' do
      captured = capture_request(:lookup_resources, [])

      client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice',
                              context: { 'region' => 'eu' }).to_a

      expect(captured.call.context.fields['region'].string_value).to eq('eu')
    end

    it 'sends no context on lookup_resources when none is supplied' do
      captured = capture_request(:lookup_resources, [])

      client.lookup_resources(SpiceDB::Consistency.full, 'document', 'view', 'user', 'alice').to_a

      expect(captured.call.context).to be_nil
    end

    it 'sends context supplied to lookup_subjects' do
      captured = capture_request(:lookup_subjects, [])

      client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user',
                             context: { 'region' => 'eu' }).to_a

      expect(captured.call.context.fields['region'].string_value).to eq('eu')
    end

    it 'sends no context on lookup_subjects when none is supplied' do
      captured = capture_request(:lookup_subjects, [])

      client.lookup_subjects(SpiceDB::Consistency.full, 'document', 'doc1', 'view', 'user').to_a

      expect(captured.call.context).to be_nil
    end
  end
end
