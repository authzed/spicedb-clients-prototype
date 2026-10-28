# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'
require 'json'

RSpec.describe 'adoption: native client behavior' do # rubocop:disable Metrics/BlockLength
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:readme) { SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme') }
  let(:viewer) { SpiceDB::Relationship.new(resource: readme, relation: 'viewer', subject: alice) }
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'token') }

  def stub_service(name, **methods)
    service = double(name.to_s)
    methods.each { |method, impl| allow(service).to receive(method, &impl) }
    client.instance_variable_set(:@proto_client, double('proto', name => service, close: nil))
    service
  end

  describe 'errors interoperate with grpc status inspection' do
    it 'is rescued as GRPC::BadStatus and reports the server code, verbatim message and cause' do
      trailer = { 'x' => 'y' }
      stub_service(:permissions, check_permission: ->(*) { raise GRPC::FailedPrecondition.new('server said no', trailer) })
      check = SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice)
      begin
        client.check(check, consistency: SpiceDB::Consistency.full)
        raise 'expected an error'
      rescue GRPC::BadStatus => e
        expect(e).to be_a(SpiceDB::FailedPreconditionError)
        expect(e.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
        expect(e.server_message).to eq('server said no')
        expect(e.message).to eq('server said no')
        expect(e.metadata).to eq(trailer)
        expect(e.cause).to be_a(GRPC::FailedPrecondition)
        expect(e.to_status.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
      end
    end

    it 'preserves codes that have no dedicated class' do
      error = SpiceDB.to_spicedb_error(GRPC::Aborted.new('try again'))
      expect(error.class).to eq(SpiceDB::Error)
      expect(error.code).to eq(GRPC::Core::StatusCodes::ABORTED)
    end

    it 'reports a status code for local validation and an exhausted local budget' do
      expect { SpiceDB::ObjectRef.new(object_type: '', object_id: 'x') }.to raise_error(GRPC::BadStatus) do |error|
        expect(error.code).to eq(GRPC::Core::StatusCodes::INVALID_ARGUMENT)
        expect(error.server_message).to be_nil
      end
      now = 0.0
      allow(Process).to receive(:clock_gettime).with(Process::CLOCK_MONOTONIC) { now }
      budget = SpiceDB::Budget.new(1)
      now = 2.0
      expect { budget.remaining }.to raise_error(SpiceDB::DeadlineExceededError) { |error| expect(error.code).to eq(GRPC::Core::StatusCodes::DEADLINE_EXCEEDED) }
    end

    it 'builds caller-constructed errors that every predicate treats like server errors' do
      error = SpiceDB::Error.from_code(GRPC::Core::StatusCodes::NOT_FOUND, 'missing')
      expect(error).to be_a(SpiceDB::NotFoundError)
      expect(error.code).to eq(GRPC::Core::StatusCodes::NOT_FOUND)
      expect(SpiceDB::AlreadyExistsError.new('dup').code).to eq(GRPC::Core::StatusCodes::ALREADY_EXISTS)
      expect(SpiceDB.code_of(error)).to eq(GRPC::Core::StatusCodes::NOT_FOUND)
    end

    it 'classifies raw grpc errors from fakes' do
      raw = GRPC::FailedPrecondition.new('raw')
      expect(SpiceDB.code_of(raw)).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
      expect(SpiceDB.error_class_of(raw)).to eq(SpiceDB::FailedPreconditionError)
      expect(SpiceDB.code_of(RuntimeError.new)).to eq(GRPC::Core::StatusCodes::UNKNOWN)
      expect(SpiceDB.code_of(nil)).to eq(GRPC::Core::StatusCodes::OK)
    end
  end

  describe 'observable values' do
    it 'formats and logs relationships, filters, updates, preconditions and transactions' do
      filter = SpiceDB::Filter.new(resource_type: 'document', relation: 'viewer',
                                   subject: SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: 'alice',
                                                                       relation: SpiceDB::SubjectRelationFilter.direct))
      txn = SpiceDB::Transaction.new.must_not_match(filter).touch(viewer)
      expect(txn.length).to eq(1)
      expect(txn.size).to eq(1)
      expect(filter.to_s).to eq('document:*#viewer@user:alice#...')
      expect(txn.updates.first.to_s).to eq('TOUCH(document:readme#viewer@user:alice)')
      expect(txn.preconditions.first.to_s).to eq('MUST_NOT_MATCH(document:*#viewer@user:alice#...)')
      expect(txn.to_s).to include('TOUCH(document:readme#viewer@user:alice)')
      json = JSON.parse(JSON.generate(txn))
      expect(json['updates'].first['relationship']).to eq('resource' => 'document:readme', 'relation' => 'viewer', 'subject' => 'user:alice')
      expect(json['preconditions'].first['filter']).to include('resource_type' => 'document', 'subject_relation' => '...')
      expect(JSON.parse(viewer.to_json)).not_to be_empty
    end

    it 'offers short relationship accessors and copy helpers' do
      expect([viewer.resource_type, viewer.resource_id, viewer.subject_type, viewer.subject_id, viewer.subject_relation])
        .to eq(['document', 'readme', 'user', 'alice', nil])
      other = SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'other')
      expect(viewer.with_resource(other).to_s).to eq('document:other#viewer@user:alice')
      expect(viewer.with_relation('editor').to_s).to eq('document:readme#editor@user:alice')
      team = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'team', object_id: 'eng'), relation: 'member')
      expect(viewer.with_subject(team).to_s).to eq('document:readme#viewer@team:eng#member')
    end
  end

  describe 'full tuple syntax' do
    # The same vectors spicedb-gen/tuplecompat checks against SpiceDB's own
    # parser and formatter for the Go client.
    [
      'document:readme#viewer@user:alice',
      'document:readme#viewer@team:eng#member',
      'document:readme#viewer@user:*',
      'document:readme#viewer@user:alice[only_name]',
      'document:readme#viewer@user:alice[ip_range:{"cidr":"10.0.0.0/8","list":["a","]"],"n":1.5,"nested":{"b":true}}]',
      'document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]',
      'document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]',
      'document:readme#viewer@team:eng#member[ip_range:{"cidr":"x"}][expiration:2030-01-02T03:04:05Z]'
    ].each do |tuple|
      it "round-trips #{tuple}" do
        expect(SpiceDB::Relationship.from_tuple(tuple).to_s).to eq(tuple)
      end
    end

    it 'parses caveat context, expiration and the ellipsis' do
      parsed = SpiceDB::Relationship.from_tuple('document:a#viewer@team:eng#...[c:{"z":1,"a":"]"}][expiration:2030-01-02T03:04:05.5Z]')
      expect(parsed.subject.relation).to be_nil
      expect(parsed.caveat.name).to eq('c')
      expect(parsed.caveat.context).to eq('z' => 1, 'a' => ']')
      expect(parsed.expiration).to eq(Time.utc(2030, 1, 2, 3, 4, 5.5r))
      expect(parsed.to_s).to eq('document:a#viewer@team:eng[c:{"a":"]","z":1}][expiration:2030-01-02T03:04:05.5Z]')
    end

    ['document:a#viewer@user:b[', 'document:a#viewer@user:b[c:{]', 'document:a#viewer@user:b[expiration:tomorrow]',
     'document:a#viewer@user:b[c]junk', 'document:a#viewer@user:b[expiration:2030-01-02T03:04:05Z][c]'].each do |bad|
      it "rejects #{bad}" do
        expect { SpiceDB::Relationship.from_tuple(bad) }.to raise_error(SpiceDB::InvalidArgumentError)
      end
    end
  end

  describe 'Filter#matches?' do
    let(:named) { SpiceDB::Relationship.from_tuple('document:readme#viewer@team:eng#member') }

    it 'applies resource, prefix, relation and subject semantics' do
      expect(SpiceDB::Filter.new(resource_type: 'document').matches?(viewer)).to be true
      expect(SpiceDB::Filter.new(resource_type: 'folder').matches?(viewer)).to be false
      expect(SpiceDB::Filter.new(resource_type: 'document', resource_id: 'other').matches?(viewer)).to be false
      expect(SpiceDB::Filter.new(resource_type: 'document', resource_id_prefix: 'read').matches?(viewer)).to be true
      expect(SpiceDB::Filter.new(resource_type: 'document', resource_id_prefix: 'x').matches?(viewer)).to be false
      expect(SpiceDB::Filter.new(resource_type: 'document', relation: 'editor').matches?(viewer)).to be false
      expect(SpiceDB::Filter.new(resource_type: 'document', subject: SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: 'bob'))
        .matches?(viewer)).to be false
    end

    it 'distinguishes Any, Direct and Named subject relations' do
      any = SpiceDB::SubjectFilter.new(subject_type: 'team')
      direct = SpiceDB::SubjectFilter.new(subject_type: 'team', relation: SpiceDB::SubjectRelationFilter.direct)
      member = SpiceDB::SubjectFilter.new(subject_type: 'team', relation: SpiceDB::SubjectRelationFilter.named('member'))
      filter = SpiceDB::Filter.new(resource_type: 'document')
      expect(filter.with_subject(any).matches?(named)).to be true
      expect(filter.with_subject(direct).matches?(named)).to be false
      expect(filter.with_subject(member).matches?(named)).to be true
      expect(viewer.to_filter.matches?(viewer.with_caveat('c'))).to be true
    end
  end

  describe 'SpiceDB::Testing::Store' do
    let(:store) { SpiceDB::Testing::Store.new }

    it 'applies preconditions, create conflicts and atomic transactions' do
      store.write(SpiceDB::Transaction.new.create(viewer))
      expect { store.write(SpiceDB::Transaction.new.touch(viewer.with_relation('editor')).create(viewer)) }
        .to raise_error(SpiceDB::AlreadyExistsError)
      expect(store.relationships.map(&:to_s)).to eq(['document:readme#viewer@user:alice'])
      expect { store.write(SpiceDB::Transaction.new.must_not_match(viewer.to_filter).delete(viewer)) }
        .to raise_error(SpiceDB::FailedPreconditionError) { |error| expect(error.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION) }
      result = store.write(SpiceDB::Transaction.new.must_match(viewer.to_filter).delete(viewer))
      expect(result.revision).to be_a(SpiceDB::ZedToken)
      expect(store.relationships).to be_empty
    end

    it 'reads and deletes with native filters, and returns registered check results' do
      store.write(SpiceDB::Transaction.new.touch(viewer).touch(viewer.with_relation('editor')))
      read = store.read_relationships(SpiceDB::Filter.new(resource_type: 'document', relation: 'editor'), consistency: SpiceDB::Consistency.full)
      expect(read.map(&:to_s)).to eq(['document:readme#editor@user:alice'])
      expect(read.first).to be_a(SpiceDB::ReadRelationship)
      expect { store.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'), limit: 1, partial: SpiceDB::DeletePartial::DISALLOWED) }
        .to raise_error(SpiceDB::InvalidArgumentError)
      expect(store.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'), limit: 5).deleted_count).to eq(2)

      check = SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice)
      store.set_check_result(check, :has_permission)
      expect(store.check(check, consistency: SpiceDB::Consistency.full).has_permission?).to be true
      other = check.with(permission: 'edit')
      expect(store.check_bulk([check, other], consistency: SpiceDB::Consistency.full).map(&:has_permission?)).to eq([true, false])
    end

    it 'has the client surface for the operations it implements' do
      %i[write read_relationships delete_relationships check check_bulk].each do |name|
        expect(SpiceDB::Testing::Store.instance_method(name).parameters).to eq(SpiceDB::Client.instance_method(name).parameters)
      end
    end
  end

  describe 'paging' do
    it 'repeats the caller consistency and page size on every page and exposes cursors and read-at' do
      requests = []
      stub_service(:permissions, read_relationships: lambda { |request, **|
        requests << [request.consistency.requirement, request.optional_limit, request.optional_cursor&.token]
        start = request.optional_cursor&.token.to_i
        FixtureOperation.new(Array.new([5 - start, 2].min) do |offset|
          Authzed::Api::V1::ReadRelationshipsResponse.new(
            relationship: SpiceDB::ProtoConv.relationship_to_proto(viewer.with_resource(SpiceDB::ObjectRef.new(object_type: 'document',
                                                                                                               object_id: "d#{start + offset}"))),
            read_at: Authzed::Api::V1::ZedToken.new(token: 'page-rev'),
            after_result_cursor: Authzed::Api::V1::Cursor.new(token: (start + offset + 1).to_s)
          )
        end)
      })
      items = client.read_relationships(SpiceDB::Filter.new(resource_type: 'document'), consistency: SpiceDB::Consistency.min_latency,
                                                                                        page_size: 2, limit: 3, cursor: '1').to_a
      expect(items.map(&:resource_id)).to eq(%w[d1 d2 d3])
      expect(items.map(&:cursor)).to eq(%w[2 3 4])
      expect(items.first.read_at).to eq(SpiceDB::ZedToken.new('page-rev'))
      expect(requests.map(&:first).uniq).to eq([:minimize_latency])
      expect(requests.map { |request| request[1] }).to eq([2, 2])
      expect(requests.map(&:last)).to eq(%w[1 3])
    end
  end

  describe 'retry policy and deadlines' do
    def failing_schema(errors)
      calls = 0
      stub_service(:schema, read_schema: lambda { |*_args, **kwargs|
        calls += 1
        @deadline = kwargs[:deadline]
        error = errors.shift
        raise error if error

        Authzed::Api::V1::ReadSchemaResponse.new(schema_text: 's')
      })
      -> { calls }
    end

    it 'opts into other read codes, including RESOURCE_EXHAUSTED, and reports each retry' do
      events = []
      policy = SpiceDB::RetryPolicy.new(max_retries: 2, retryable_codes: [GRPC::Core::StatusCodes::RESOURCE_EXHAUSTED],
                                        initial_backoff: 0.001, on_retry: ->(event) { events << event })
      client.instance_variable_set(:@retry_policy, policy)
      calls = failing_schema([GRPC::ResourceExhausted.new('busy'), GRPC::ResourceExhausted.new('busy')])
      expect(client.read_schema.schema).to eq('s')
      expect(calls.call).to eq(3)
      expect(events.map(&:attempt)).to eq([1, 2])
      expect(events.first.operation).to eq('read_schema')
      expect(events.first.error).to be_a(SpiceDB::ResourceExhaustedError)
    end

    it 'disables retries entirely' do
      disabled = SpiceDB::Client.new_plaintext('localhost:50051', 'token', retries: SpiceDB::Retries::DISABLED)
      service = double('schema')
      calls = 0
      allow(service).to receive(:read_schema) do
        calls += 1
        raise GRPC::Unavailable, 'down'
      end
      disabled.instance_variable_set(:@proto_client, double('proto', schema: service))
      expect { disabled.read_schema }.to raise_error(SpiceDB::UnavailableError)
      expect(calls).to eq(1)
    end

    it 'never replays a mutation under any policy' do
      policy = SpiceDB::RetryPolicy.new(retryable_codes: [GRPC::Core::StatusCodes::UNAVAILABLE], initial_backoff: 0.001)
      client.instance_variable_set(:@retry_policy, policy)
      calls = 0
      stub_service(:permissions, write_relationships: lambda { |*|
        calls += 1
        raise GRPC::Unavailable, 'down'
      })
      expect { client.write(SpiceDB::Transaction.new.touch(viewer)) }.to raise_error(SpiceDB::UnavailableError)
      expect(calls).to eq(1)
    end

    it 'can disable stream establishment retries' do
      no_stream_retry = SpiceDB::Client.new_plaintext('localhost:50051', 'token', stream_retries: SpiceDB::StreamRetries::DISABLED)
      calls = 0
      service = double('permissions')
      allow(service).to receive(:lookup_subjects) do
        calls += 1
        raise GRPC::Unavailable, 'down'
      end
      no_stream_retry.instance_variable_set(:@proto_client, double('proto', permissions: service))
      expect do
        no_stream_retry.lookup_subjects(resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a
      end.to raise_error(SpiceDB::UnavailableError)
      expect(calls).to eq(1)
    end

    it 'disables the default deadline with a named mode' do
      unbounded = SpiceDB::Client.new_plaintext('localhost:50051', 'token', default_deadline: SpiceDB::DefaultDeadline::DISABLED)
      deadlines = []
      service = double('schema')
      allow(service).to receive(:read_schema) do |_request, **kwargs|
        deadlines << kwargs[:deadline]
        Authzed::Api::V1::ReadSchemaResponse.new
      end
      unbounded.instance_variable_set(:@proto_client, double('proto', schema: service))
      unbounded.read_schema
      unbounded.read_schema(timeout: 5)
      expect(deadlines.first).to be_nil
      expect(deadlines.last).to be_a(Time)
      expect { SpiceDB::Client.new_plaintext('localhost:50051', 'token', default_deadline: false) }.to raise_error(SpiceDB::InvalidArgumentError)
    end

    it 'validates retry policies' do
      expect { SpiceDB::RetryPolicy.new(max_retries: -1) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { SpiceDB::RetryPolicy.new(initial_backoff: 0) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  describe 'bulk item errors' do
    it 'returns per-item errors on results in RETURNED mode and never grants them' do
      stub_service(:permissions, check_bulk_permissions: lambda { |request, **|
        Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: request.items.each_with_index.map do |item, index|
          if index.zero?
            Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, error: Google::Rpc::Status.new(code: 3, message: 'bad item'))
          else
            Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, item: Authzed::Api::V1::CheckBulkPermissionsResponseItem.new(
              permissionship: :PERMISSIONSHIP_HAS_PERMISSION
            ))
          end
        end)
      })
      check = SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice)
      results = client.check_bulk([check, check], consistency: SpiceDB::Consistency.full, item_errors: SpiceDB::BulkItemErrors::RETURNED)
      expect(results.map(&:has_permission?)).to eq([false, true])
      expect(results.first.error).to be_a(SpiceDB::InvalidArgumentError)
      expect(results.first.error.input_index).to eq(0)
      expect(results.first.permissionship).to eq(:unspecified)
      expect { client.check_bulk([check, check], consistency: SpiceDB::Consistency.full) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  describe 'imports, deletes and call options' do
    it 'sends relationships in batches of the configured size' do
      sizes = []
      stub_service(:permissions, import_bulk_relationships: lambda { |requests, **|
        requests.each { |request| sizes << request.relationships.length }
        FixtureOperation.new(Authzed::Api::V1::ImportBulkRelationshipsResponse.new(num_loaded: sizes.sum))
      })
      expect(client.import_relationships(Array.new(5) { viewer }, batch_size: 2)).to eq(5)
      expect(sizes).to eq([2, 2, 1])
      expect { client.import_relationships([viewer], batch_size: 0) }.to raise_error(SpiceDB::InvalidArgumentError)
    end

    it 'supports all-or-nothing deletes without a limit and requires a limit otherwise' do
      wires = []
      stub_service(:permissions, delete_relationships: lambda { |request, **|
        wires << request
        Authzed::Api::V1::DeleteRelationshipsResponse.new(deletion_progress: :DELETION_PROGRESS_COMPLETE, relationships_deleted_count: 1)
      })
      filter = SpiceDB::Filter.new(resource_type: 'document')
      client.delete_relationships(filter, partial: SpiceDB::DeletePartial::DISALLOWED)
      expect(wires.last.optional_allow_partial_deletions).to be false
      expect(wires.last.optional_limit).to eq(0)
      expect { client.delete_relationships(filter) }.to raise_error(SpiceDB::InvalidArgumentError)
      client.delete_relationships(filter, limit: 3)
      expect(wires.last.optional_allow_partial_deletions).to be true
    end

    it 'reports cumulative delete-all progress after every batch' do
      responses = [[2, :DELETION_PROGRESS_PARTIAL, 'c1'], [1, :DELETION_PROGRESS_COMPLETE, nil]]
      stub_service(:permissions, delete_relationships: lambda { |*|
        count, progress, cursor = responses.shift
        Authzed::Api::V1::DeleteRelationshipsResponse.new(relationships_deleted_count: count, deletion_progress: progress,
                                                          deleted_at: Authzed::Api::V1::ZedToken.new(token: "r#{count}"),
                                                          after_result_cursor: cursor && Authzed::Api::V1::Cursor.new(token: cursor))
      })
      seen = []
      client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document'), on_progress: ->(result) { seen << result })
      expect(seen.map(&:deleted_count)).to eq([2, 3])
      expect(seen.first.cursor).to eq('c1')
    end

    it 'passes per-call metadata to every operation' do
      seen = []
      stub_service(:permissions, check_permission: lambda { |_request, **kwargs|
        seen << kwargs[:metadata]
        Authzed::Api::V1::CheckPermissionResponse.new(permissionship: :PERMISSIONSHIP_HAS_PERMISSION)
      })
      check = SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice)
      client.check(check, consistency: SpiceDB::Consistency.full, call_options: SpiceDB::CallOptions.new(metadata: { 'x-request-id' => 'r1' }))
      expect(seen).to eq([{ 'x-request-id' => 'r1' }])
    end

    it 'accepts call_options on every public operation' do
      operations = SpiceDB::Client.public_instance_methods(false) - %i[close proto_client retry_policy]
      missing = operations.reject { |name| SpiceDB::Client.instance_method(name).parameters.include?(%i[key call_options]) }
      expect(missing).to eq([])
    end
  end

  describe 'SpiceDB::ProtoConv' do
    it 'round-trips relationships, filters and updates' do
      rel = SpiceDB::Relationship.from_tuple('document:a#viewer@team:eng#member[c:{"k":"v"}][expiration:2030-01-02T03:04:05Z]')
      expect(SpiceDB::ProtoConv.relationship_from_proto(SpiceDB::ProtoConv.relationship_to_proto(rel))).to eq(rel)
      filter = rel.to_filter
      expect(SpiceDB::ProtoConv.filter_from_proto(SpiceDB::ProtoConv.filter_to_proto(filter))).to eq(filter)
      update = SpiceDB::Update.new(operation: :touch, relationship: rel)
      expect(SpiceDB::ProtoConv.update_from_proto(SpiceDB::ProtoConv.update_to_proto(update))).to eq(update)
      expect(SpiceDB::ProtoConv.consistency_to_proto(SpiceDB::Consistency.full).fully_consistent).to be true
    end

    it 'converts check results with nested traces back to protocol messages' do
      wire = Authzed::Api::V1::CheckPermissionResponse.new(
        checked_at: Authzed::Api::V1::ZedToken.new(token: 'rev'), permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
        debug_trace: Authzed::Api::V1::DebugInformation.new(schema_used: 'schema', check: Authzed::Api::V1::CheckDebugTrace.new(
          resource: SpiceDB::ProtoConv.object_to_proto(readme), permission: 'view', permission_type: :PERMISSION_TYPE_PERMISSION,
          subject: SpiceDB::ProtoConv.subject_to_proto(alice), result: :PERMISSIONSHIP_HAS_PERMISSION,
          duration: Google::Protobuf::Duration.new(seconds: 1, nanos: 500),
          sub_problems: Authzed::Api::V1::CheckDebugTrace::SubProblems.new(traces: [Authzed::Api::V1::CheckDebugTrace.new(
            permission: 'viewer', was_cached_result: false, result: :PERMISSIONSHIP_HAS_PERMISSION
          )])
        ))
      )
      native = SpiceDB::ProtoConv.check_result_from_proto(wire)
      expect(SpiceDB::ProtoConv.check_result_to_proto(native)).to eq(wire)
      expect(native.debug_trace.check.subproblems.first.was_cached_result).to be false
    end
  end
end
