# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Per-call retry policies (root DESIGN.md "Retry only when replay is safe"),
# empty writes, single-request paging and wrapper-free error statuses.
RSpec.describe 'per-call retry policy, empty writes and single-request paging' do
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:readme) { SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme') }
  let(:viewer) { SpiceDB::Relationship.new(resource: readme, relation: 'viewer', subject: alice) }
  let(:filter) { SpiceDB::Filter.new(resource_type: 'document') }
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'token') }
  let(:retry_once) { SpiceDB::RetryPolicy.new(max_retries: 1, retryable_codes: [GRPC::Core::StatusCodes::UNAVAILABLE], initial_backoff: 0.001) }
  let(:no_retries) { SpiceDB::RetryPolicy.new(max_retries: 0) }

  # Installs services whose methods fail with UNAVAILABLE on their first
  # call and then succeed (or call +impl+); returns the per-method call counts.
  def flaky(target = client, **services)
    calls = Hash.new(0)
    doubles = services.to_h do |name, methods|
      service = double(name.to_s)
      methods.each do |method, impl|
        allow(service).to receive(method) do |*args, **kwargs|
          calls[method] += 1
          raise GRPC::Unavailable, 'transient' if calls[method] == 1

          impl.call(*args, **kwargs)
        end
      end
      [name, service]
    end
    target.instance_variable_set(:@proto_client, double('proto', close: nil, **doubles))
    calls
  end

  let(:write_ok) { ->(*, **) { Authzed::Api::V1::WriteRelationshipsResponse.new(written_at: Authzed::Api::V1::ZedToken.new(token: 'w')) } }

  describe 'mutations' do
    it 'never retries a write by default' do
      calls = flaky(permissions: { write_relationships: write_ok })
      expect { client.write(SpiceDB::Transaction.new.touch(viewer)) }.to raise_error(SpiceDB::UnavailableError)
      expect(calls[:write_relationships]).to eq(1)
    end

    it 'lets a touch-only write without preconditions opt in for one call' do
      calls = flaky(permissions: { write_relationships: write_ok })
      result = client.write(SpiceDB::Transaction.new.touch(viewer), retry_policy: retry_once)
      expect(result.revision.to_s).to eq('w')
      expect(calls[:write_relationships]).to eq(2)
    end

    it 'lets a schema write opt in for one call' do
      calls = flaky(schema: { write_schema: ->(*, **) { Authzed::Api::V1::WriteSchemaResponse.new(written_at: Authzed::Api::V1::ZedToken.new(token: 's')) } })
      expect(client.write_schema('definition user {}', retry_policy: retry_once).to_s).to eq('s')
      expect(calls[:write_schema]).to eq(2)
    end

    it 'rejects the opt-in on every other mutation before any request' do
      calls = flaky(permissions: { write_relationships: write_ok }, experimental: {})
      creates = SpiceDB::Transaction.new.create(viewer)
      deletes = SpiceDB::Transaction.new.delete(viewer)
      guarded = SpiceDB::Transaction.new.must_match(filter).touch(viewer)
      [creates, deletes, guarded].each do |txn|
        expect { client.write(txn, retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError, /cannot be retried/)
      end
      expect { client.delete_relationships(filter, limit: 1, retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.delete_all_relationships_non_atomic(filter, retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.import_relationships([viewer], retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.experimental_register_relationship_counter('c', filter, retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.experimental_unregister_relationship_counter('c', retry_policy: retry_once) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect(calls).to be_empty
    end

    it 'accepts a policy without retries on any mutation' do
      calls = flaky(permissions: { write_relationships: write_ok })
      expect { client.write(SpiceDB::Transaction.new.create(viewer), retry_policy: no_retries) }.to raise_error(SpiceDB::UnavailableError)
      expect(calls[:write_relationships]).to eq(1)
    end

    it 'rejects a retry_policy that is not a RetryPolicy' do
      expect { client.write(SpiceDB::Transaction.new.touch(viewer), retry_policy: 3) }.to raise_error(SpiceDB::InvalidArgumentError)
      expect { client.read_schema(retry_policy: {}) }.to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  describe 'reads and streams' do
    it 'overrides the client policy for one call, including disabled retries' do
      disabled = SpiceDB::Client.new_plaintext('localhost:50051', 'token', retries: SpiceDB::Retries::DISABLED)
      calls = flaky(disabled, schema: { read_schema: ->(*, **) { Authzed::Api::V1::ReadSchemaResponse.new(schema_text: 's') } })
      events = []
      policy = retry_once.with(on_retry: ->(event) { events << event })
      expect(disabled.read_schema(retry_policy: policy).schema).to eq('s')
      expect(calls[:read_schema]).to eq(2)
      expect(events.map(&:operation)).to eq(['read_schema'])
    end

    it 'exempts one stream while the client retries others' do
      response = ->(*, **) { FixtureOperation.new([]) }
      calls = flaky(permissions: { lookup_subjects: response, read_relationships: response })
      lookup = { resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full }
      expect { client.lookup_subjects(**lookup, retry_policy: no_retries).to_a }.to raise_error(SpiceDB::UnavailableError)
      expect(calls[:lookup_subjects]).to eq(1)
      expect(client.read_relationships(filter, consistency: SpiceDB::Consistency.full).to_a).to eq([])
      expect(calls[:read_relationships]).to eq(2)
    end

    it 'never re-establishes watch unless the call passes a policy' do
      calls = flaky(watch: { watch: ->(*, **) { FixtureOperation.new([]) } })
      expect { client.watch.to_a }.to raise_error(SpiceDB::UnavailableError)
      expect(calls[:watch]).to eq(1)
      calls = flaky(watch: { watch: ->(*, **) { FixtureOperation.new([]) } })
      expect(client.watch(retry_policy: retry_once).to_a).to eq([])
      expect(calls[:watch]).to eq(2)
    end

    it 'gives every public operation a retry_policy keyword' do
      operations = SpiceDB::Client.public_instance_methods(false) - %i[close proto_client retry_policy]
      missing = operations.reject { |name| SpiceDB::Client.instance_method(name).parameters.include?(%i[key retry_policy]) }
      expect(missing).to eq([])
    end
  end

  describe 'empty writes' do
    it 'sends an empty transaction and returns the revision' do
      requests = []
      client.instance_variable_set(:@proto_client, double('proto', permissions: double('permissions').tap do |service|
        allow(service).to receive(:write_relationships) do |request, **|
          requests << request
          write_ok.call
        end
      end))
      expect(client.write(SpiceDB::Transaction.new).revision.to_s).to eq('w')
      expect(requests.length).to eq(1)
      expect(requests.first.updates).to be_empty
    end
  end

  describe 'single-request paging' do
    def lookup_stub
      requests = []
      service = double('permissions')
      allow(service).to receive(:lookup_resources) do |request, **|
        requests << request
        FixtureOperation.new(Array.new(3) do |index|
          Authzed::Api::V1::LookupResourcesResponse.new(resource_object_id: index.to_s, permissionship: :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                                                        after_result_cursor: Authzed::Api::V1::Cursor.new(token: "c#{index}"))
        end)
      end
      allow(service).to receive(:read_relationships) do |request, **|
        requests << request
        FixtureOperation.new(Array.new(2) do |index|
          Authzed::Api::V1::ReadRelationshipsResponse.new(relationship: SpiceDB::ProtoConv.relationship_to_proto(viewer),
                                                          after_result_cursor: Authzed::Api::V1::Cursor.new(token: "r#{index}"))
        end)
      end
      client.instance_variable_set(:@proto_client, double('proto', permissions: service))
      requests
    end

    it 'sends exactly one unbounded lookup request without a limit' do
      requests = lookup_stub
      items = client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full,
                                      page_size: 3, paging: SpiceDB::Paging::SINGLE_REQUEST).to_a
      expect(items.map(&:cursor)).to eq(%w[c0 c1 c2])
      expect(requests.length).to eq(1), 'a full page never fetches a follow-up page'
      expect(requests.first.optional_limit).to eq(0)
    end

    it 'sends the total limit as the one read request limit and ignores page size' do
      requests = lookup_stub
      items = client.read_relationships(filter, consistency: SpiceDB::Consistency.full, page_size: 1, limit: 2,
                                                paging: SpiceDB::Paging::SINGLE_REQUEST).to_a
      expect(items.map(&:cursor)).to eq(%w[r0 r1])
      expect(requests.map(&:optional_limit)).to eq([2])
    end

    it 'validates the paging mode' do
      lookup_stub
      expect { client.read_relationships(filter, consistency: SpiceDB::Consistency.full, paging: :single).to_a }
        .to raise_error(SpiceDB::InvalidArgumentError)
    end
  end

  describe 'item and progress errors' do
    it 'report the server message once through the status accessor' do
      service = double('permissions')
      allow(service).to receive(:check_bulk_permissions) do |request, **|
        Authzed::Api::V1::CheckBulkPermissionsResponse.new(pairs: request.items.map do |item|
          Authzed::Api::V1::CheckBulkPermissionsPair.new(request: item, error: Google::Rpc::Status.new(code: 9, message: 'no such permission'))
        end)
      end
      allow(service).to receive(:delete_relationships) { raise GRPC::Unavailable, 'gone' }
      client.instance_variable_set(:@proto_client, double('proto', permissions: service))

      check = SpiceDB::PermissionCheck.new(resource: readme, permission: 'view', subject: alice)
      item = client.check_bulk([check], consistency: SpiceDB::Consistency.full, item_errors: SpiceDB::BulkItemErrors::RETURNED).first.error
      expect(item.input_index).to eq(0)
      expect(item.to_status.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
      expect(item.to_status.details).to eq('no such permission')
      expect(item.message).to eq('no such permission')

      expect { client.delete_all_relationships_non_atomic(filter) }.to raise_error(SpiceDB::UnavailableError) do |error|
        expect(error.delete_progress.deleted_count).to eq(0)
        expect(error.to_status.details).to eq('gone')
        expect(error.message).to eq('gone')
      end
    end
  end
end
