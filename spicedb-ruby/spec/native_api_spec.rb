# frozen_string_literal: true

require_relative '../lib/spicedb'

RSpec.describe 'native request values' do
  it 'snapshots nested JSON and separates check context from stored caveats' do
    context = { 'nested' => [nil, { 'value' => +'original' }] }
    resource = SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'one')
    subject = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    check = SpiceDB::PermissionCheck.new(resource: resource, permission: 'view', subject: subject, context: context)
    context['nested'][1]['value'].replace('changed')
    expect(check.context['nested'][1]['value']).to eq('original')
    expect { check.context['nested'] << 1 }.to raise_error(FrozenError)
    relationship = SpiceDB::Relationship.new(resource: resource, relation: 'viewer', subject: subject)
    expect(relationship).not_to respond_to(:check_context)
    expect(relationship.to_filter.subject.relation).to eq(SpiceDB::SubjectRelationFilter.direct)
  end

  it 'rejects cyclic and non-JSON context at construction with a path' do
    resource = SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'one')
    subject = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    cyclic = {}
    cyclic['cycle'] = cyclic
    [cyclic, { 'bad' => Object.new }, { symbol: 1 }, { 'bad' => Float::INFINITY }].each do |context|
      expect do
        SpiceDB::PermissionCheck.new(resource: resource, permission: 'view', subject: subject, context: context)
      end.to raise_error(SpiceDB::InvalidArgumentError, /context/)
    end
  end
end

require 'spicedb_proto'

RSpec.describe 'singular check tracing' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:request) do
    SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'one'),
                                 permission: 'view', subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')),
                                 context: { 'item' => nil })
  end

  after { client.close }

  it 'uses CheckPermission and preserves cached=false, recursive traces and caveat JSON' do
    caveat = Authzed::Api::V1::CaveatEvalInfo.new(
      expression: 'x', result: :RESULT_MISSING_SOME_CONTEXT, caveat_name: 'condition',
      context: Google::Protobuf::Struct.decode_json('{"x":[null,3]}'),
      partial_caveat_info: Authzed::Api::V1::PartialCaveatInfo.new(missing_required_context: ['other'])
    )
    trace = Authzed::Api::V1::CheckDebugTrace.new(
      permission: 'view', permission_type: :PERMISSION_TYPE_PERMISSION,
      result: :PERMISSIONSHIP_CONDITIONAL_PERMISSION, was_cached_result: false,
      duration: Google::Protobuf::Duration.new(seconds: 1, nanos: 5), trace_operation_id: 'op', source: 'node',
      caveat_evaluation_info: caveat
    )
    parent = Authzed::Api::V1::CheckDebugTrace.new(sub_problems: Authzed::Api::V1::CheckDebugTrace::SubProblems.new(traces: [trace]))
    response = Authzed::Api::V1::CheckPermissionResponse.new(permissionship: :PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                                                             checked_at: Authzed::Api::V1::ZedToken.new(token: 'revision'),
                                                             debug_trace: Authzed::Api::V1::DebugInformation.new(
                                                               schema_used: 'schema', check: parent
                                                             ))
    expect(client.proto_client.permissions).to receive(:check_permission) do |wire, **options|
      expect(wire.with_tracing).to be(true)
      expect(wire.context.fields['default'].bool_value).to be(true)
      expect(wire.context.fields['item'].kind).to eq(:null_value)
      expect(options[:deadline]).to be > Time.now
      response
    end
    expect(client.proto_client.permissions).not_to receive(:check_bulk_permissions)
    result = client.check(request, consistency: SpiceDB::Consistency.full, tracing: SpiceDB::Tracing::ENABLED, context: { 'default' => true, 'item' => 1 })
    expect(result.has_permission?).to be(false)
    child = result.debug_trace.check.subproblems.first
    expect(child.was_cached_result).to be(false)
    expect(child.resolution).to eq(:cached)
    expect(child.duration).to eq(1 + Rational(5, 1_000_000_000))
    expect(child.caveat_evaluation.context).to eq('x' => [nil, 3])
    expect(child.caveat_evaluation.missing_context).to eq(['other'])
    expect(child.trace_operation_id).to eq('op')
    expect(child.source).to eq('node')
    expect(child.caveat_evaluation.context['x']).to be_frozen
  end

  it 'preserves singular expiration without requesting a trace' do
    response = Authzed::Api::V1::CheckPermissionResponse.new(
      permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
      optional_expires_at: Google::Protobuf::Timestamp.new(seconds: 100, nanos: 123)
    )
    expect(client.proto_client.permissions).to receive(:check_permission).and_return(response)
    result = client.check(request, consistency: SpiceDB::Consistency.full)
    expect(result.expiration).to eq(Time.at(100, 123, :nsec))
    expect(result.expiration).to be_frozen
    expect(result.debug_trace).to be_nil
    bulk = Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: :PERMISSIONSHIP_NO_PERMISSION)
    expect(client.send(:check_result, bulk, nil).expiration).to be_nil
  end

  it 'leaves tracing disabled and missing trace absent by default' do
    expect(client.proto_client.permissions).to receive(:check_permission) do |wire, **|
      expect(wire.with_tracing).to be(false)
      Authzed::Api::V1::CheckPermissionResponse.new(permissionship: :PERMISSIONSHIP_HAS_PERMISSION)
    end
    result = client.check(request, consistency: SpiceDB::Consistency.full)
    expect(result.debug_trace).to be_nil
    expect(result.expiration).to be_nil
    expect(result.has_permission?).to be(true)
  end
end

RSpec.describe 'bulk response identity' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  after { client.close }

  %i[missing reversed duplicated].each do |malformation|
    it "rejects #{malformation} echoed requests before returning authorization decisions" do
      checks = %w[one two].map do |id|
        SpiceDB::PermissionCheck.new(
          resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: id), permission: 'view',
          subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
        )
      end
      expect(client.proto_client.permissions).to receive(:check_bulk_permissions) do |wire, **|
        requests = case malformation
                   when :missing then [nil, nil]
                   when :reversed then wire.items.to_a.reverse
                   else [wire.items.first, wire.items.first]
                   end
        Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: requests.map do |request|
          Authzed::Api::V1::CheckBulkPermissionsPair.new(
            request: request, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(permissionship: :PERMISSIONSHIP_HAS_PERMISSION)
          )
        end)
      end
      expect { client.check_bulk(checks, consistency: SpiceDB::Consistency.full) }.to raise_error(SpiceDB::Error, /echoed request/)
    end
  end
end

RSpec.describe 'operation budgets and bounded deletion' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }
  let(:filter) { SpiceDB::Filter.new(resource_type: 'document') }
  after { client.close }

  it 'shares one total budget across bulk chunks and never starts an expired chunk' do
    now = 0.0
    allow(Process).to receive(:clock_gettime).with(Process::CLOCK_MONOTONIC) { now }
    request = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'one'),
                                           permission: 'view',
                                           subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')))
    deadlines = []
    allow(client.proto_client.permissions).to receive(:check_bulk_permissions) do |wire, **options|
      deadlines << (options[:deadline] - Time.now)
      now += 6
      Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: wire.items.map do |item|
        Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
          permissionship: :PERMISSIONSHIP_NO_PERMISSION
        ))
      end)
    end
    expect { client.check_bulk(Array.new(2001, request), consistency: SpiceDB::Consistency.full, timeout: 10) }
      .to raise_error(SpiceDB::DeadlineExceededError)
    expect(deadlines.length).to eq(2)
    expect(deadlines[0]).to be_within(0.2).of(10)
    expect(deadlines[1]).to be_within(0.2).of(4)
  end

  it 'returns after one partial bounded delete without silently paging' do
    expect(client.proto_client.permissions).to receive(:delete_relationships).once do |wire, **|
      expect(wire.optional_limit).to eq(2)
      Authzed::Api::V1::DeleteRelationshipsResponse.new(deleted_at: Authzed::Api::V1::ZedToken.new(token: 'ack'),
                                                        relationships_deleted_count: 2, deletion_progress: :DELETION_PROGRESS_PARTIAL)
    end
    result = client.delete_relationships(filter, limit: 2)
    expect(result.deleted_count).to eq(2)
    expect(result.progress).to eq(:partial)
    expect(result.cursor).to be_nil
  end

  it 'continues delete-all without a cursor and preserves acknowledged progress on failure' do
    calls = 0
    allow(client.proto_client.permissions).to receive(:delete_relationships) do |wire, **|
      calls += 1
      expect(wire.optional_cursor).to be_nil
      raise GRPC::Unavailable, 'response lost' if calls == 2

      Authzed::Api::V1::DeleteRelationshipsResponse.new(deleted_at: Authzed::Api::V1::ZedToken.new(token: 'ack'),
                                                        relationships_deleted_count: 2, deletion_progress: :DELETION_PROGRESS_PARTIAL)
    end
    expect { client.delete_all_relationships_non_atomic(filter, page_size: 2) }.to raise_error(SpiceDB::UnavailableError) do |error|
      expect(error.delete_progress.deleted_count).to eq(2)
      expect(error.delete_progress.revision).to eq(SpiceDB::ZedToken.new('ack'))
      expect(error.cause).to be_a(GRPC::Unavailable)
    end
    expect(calls).to eq(2)
  end

  it 'retains the latest acknowledged deletion when a later response has unknown progress' do
    responses = [[2, :DELETION_PROGRESS_PARTIAL, 'first'], [3, 99, 'last']].map do |count, state, token|
      Authzed::Api::V1::DeleteRelationshipsResponse.new(
        deleted_at: Authzed::Api::V1::ZedToken.new(token: token), relationships_deleted_count: count, deletion_progress: state
      )
    end
    expect(client.proto_client.permissions).to receive(:delete_relationships).twice.and_return(*responses)
    expect { client.delete_all_relationships_non_atomic(filter, page_size: 3) }.to raise_error(SpiceDB::Error, /unknown progress/) do |error|
      expect(error.delete_progress.deleted_count).to eq(5)
      expect(error.delete_progress.revision).to eq(SpiceDB::ZedToken.new('last'))
      expect(error.delete_progress.progress).to eq(:unspecified)
      expect(error.code).to eq(GRPC::Core::StatusCodes::INTERNAL)
    end
  end

  it 'distinguishes Any, Direct and Named subject relation filters on the wire' do
    [SpiceDB::SubjectRelationFilter.any, SpiceDB::SubjectRelationFilter.direct, SpiceDB::SubjectRelationFilter.named('member')].each do |relation|
      native = filter.with_subject(SpiceDB::SubjectFilter.new(subject_type: 'group', relation: relation))
      wire = client.send(:filter_to_proto, native).optional_subject_filter
      if relation.kind == :any
        expect(wire.optional_relation).to be_nil
      else
        expect(wire.optional_relation.relation).to eq(relation.name || '')
      end
    end
  end
end

RSpec.describe 'explicit non-atomic deletion API' do
  it 'exposes only the explicit operation name' do
    expect(SpiceDB::Client.instance_methods).to include(:delete_all_relationships_non_atomic)
    expect(SpiceDB::Client.instance_methods).not_to include(:delete_all_relationships)
  end
end
