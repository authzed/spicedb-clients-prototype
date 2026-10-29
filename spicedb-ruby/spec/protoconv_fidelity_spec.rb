# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Conversions to response messages reproduce the server's wire shape: a
# server response is read through the client and converted back unchanged.
RSpec.describe 'SpiceDB::ProtoConv wire fidelity' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'token') }
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }

  def check_for(id, context = nil)
    SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: id), permission: 'view',
                                 subject: alice, context: context)
  end

  def serve(**methods)
    service = double('permissions')
    methods.each { |method, impl| allow(service).to receive(method, &impl) }
    client.instance_variable_set(:@proto_client, double('proto', permissions: service))
  end

  it 'round-trips a whole bulk-check response, including per-item errors' do
    served = nil
    serve(check_bulk_permissions: lambda { |request, **|
      served = Authzed::Api::V1::CheckBulkPermissionsResponse.new(
        checked_at: Authzed::Api::V1::ZedToken.new(token: 'bulk-rev'),
        pairs: request.items.map do |item|
          if item.resource.object_id == 'broken'
            Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, error: Google::Rpc::Status.new(code: 9, message: 'permission view not found'))
          else
            Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
              permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION,
              partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['cidr'])
            ))
          end
        end
      )
    })
    checks = [check_for('a'), check_for('broken'), check_for('c', { 'cidr' => '10.0.0.0/8' })]
    results = client.check_bulk(checks, consistency: SpiceDB::Consistency.full, context: { 'tenant' => 't1' },
                                        item_errors: SpiceDB::BulkItemErrors::RETURNED)

    converted = SpiceDB::ProtoConv.check_bulk_to_proto(checks, results, context: { 'tenant' => 't1' })
    expect(converted).to eq(served)
    expect(SpiceDB::ProtoConv.check_bulk_request_item_to_proto(checks[2], context: { 'tenant' => 't1' })).to eq(served.pairs[2].request)
    expect { SpiceDB::ProtoConv.check_bulk_to_proto(checks.first(1), results) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'keeps an item error reason as an ErrorInfo detail' do
    error = SpiceDB::Error.from_code(9, 'nope', server_message: 'nope', reason: 'ERROR_REASON_UNKNOWN_RELATION_OR_PERMISSION',
                                                reason_domain: 'authzed.com')
    status = SpiceDB::ProtoConv.status_to_proto(error)
    expect(SpiceDB.to_spicedb_error(status).reason).to eq('ERROR_REASON_UNKNOWN_RELATION_OR_PERMISSION')
    expect(status.message).to eq('nope')
  end

  it 'round-trips a lookup-subjects response with the deprecated fields SpiceDB still sends' do
    served = Authzed::Api::V1::LookupSubjectsResponse.new(
      looked_up_at: Authzed::Api::V1::ZedToken.new(token: 'rev3'),
      subject: Authzed::Api::V1::ResolvedSubject.new(subject_object_id: '*', permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION),
      subject_object_id: '*', permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
      excluded_subjects: [Authzed::Api::V1::ResolvedSubject.new(
        subject_object_id: 'bob', permissionship: :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
        partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['k'])
      )],
      excluded_subject_ids: ['bob']
    )
    serve(lookup_subjects: ->(*, **) { FixtureOperation.new([served]) })
    subject = client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'a'), permission: 'view',
                                     subject_type: 'user', consistency: SpiceDB::Consistency.full).first
    expect(SpiceDB::ProtoConv.lookup_subject_to_proto(subject)).to eq(served)
  end
end
