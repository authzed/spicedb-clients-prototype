# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative 'support/native_helpers'
require 'spicedb_proto'
require 'grpc'

# Client-wide per-operation retry policies (root DESIGN.md "Retry only when
# replay is safe"): per-call, then per-operation, then client-wide.
RSpec.describe 'operation_retry_policies' do
  let(:alice) { SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice')) }
  let(:readme) { SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'readme') }
  let(:viewer) { SpiceDB::Relationship.new(resource: readme, relation: 'viewer', subject: alice) }
  let(:filter) { SpiceDB::Filter.new(resource_type: 'document') }
  let(:retry_once) { SpiceDB::RetryPolicy.new(max_retries: 1, retryable_codes: [GRPC::Core::StatusCodes::UNAVAILABLE], initial_backoff: 0.001) }
  let(:no_retries) { SpiceDB::RetryPolicy.new(max_retries: 0) }
  let(:write_ok) { ->(*, **) { Authzed::Api::V1::WriteRelationshipsResponse.new(written_at: Authzed::Api::V1::ZedToken.new(token: 'w')) } }
  let(:schema_ok) { ->(*, **) { Authzed::Api::V1::ReadSchemaResponse.new(schema_text: 's') } }
  let(:empty_stream) { ->(*, **) { FixtureOperation.new([]) } }

  def client_with(**options) = SpiceDB::Client.new_plaintext('localhost:50051', 'token', **options)

  # Installs services whose methods fail with UNAVAILABLE on their first
  # call and then call +impl+; returns the per-method call counts.
  def flaky(target, **services)
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

  it 'never reopens a watch, with no per-call option, while other operations keep the client policy' do
    never = client_with(retry_policy: retry_once, operation_retry_policies: { watch: no_retries, read_relationships: no_retries })
    calls = flaky(never, watch: { watch: empty_stream }, permissions: { read_relationships: empty_stream, lookup_subjects: empty_stream },
                         schema: { read_schema: schema_ok })
    expect { never.watch.to_a }.to raise_error(SpiceDB::UnavailableError)
    expect(calls[:watch]).to eq(1)
    expect { never.read_relationships(filter, consistency: SpiceDB::Consistency.full).to_a }.to raise_error(SpiceDB::UnavailableError)
    expect(calls[:read_relationships]).to eq(1)
    lookup = { resource: readme, permission: 'view', subject_type: 'user', consistency: SpiceDB::Consistency.full }
    expect(never.lookup_subjects(**lookup).to_a).to eq([])
    expect(calls[:lookup_subjects]).to eq(2)
    expect(never.read_schema.schema).to eq('s')
    expect(calls[:read_schema]).to eq(2)
  end

  it 'governs stream establishment and applies under disabled retries' do
    reopen = client_with(retries: SpiceDB::Retries::DISABLED, stream_retries: SpiceDB::StreamRetries::DISABLED,
                         operation_retry_policies: { watch: retry_once, read_schema: retry_once })
    calls = flaky(reopen, watch: { watch: empty_stream }, schema: { read_schema: schema_ok })
    expect(reopen.watch.to_a).to eq([])
    expect(calls[:watch]).to eq(2)
    expect(reopen.read_schema.schema).to eq('s')
    expect(calls[:read_schema]).to eq(2)
  end

  it 'yields to a per-call policy' do
    never = client_with(operation_retry_policies: { watch: no_retries, write: retry_once })
    calls = flaky(never, watch: { watch: empty_stream }, permissions: { write_relationships: write_ok })
    expect(never.watch(retry_policy: retry_once).to_a).to eq([])
    expect(calls[:watch]).to eq(2)
    expect { never.write(SpiceDB::Transaction.new.touch(viewer), retry_policy: no_retries) }.to raise_error(SpiceDB::UnavailableError)
    expect(calls[:write_relationships]).to eq(1)
  end

  it 'retries only idempotent writes, leaving others unretried without an error' do
    writes = client_with(operation_retry_policies: { write: retry_once })
    calls = flaky(writes, permissions: { write_relationships: write_ok })
    expect(writes.write(SpiceDB::Transaction.new.touch(viewer)).revision.to_s).to eq('w')
    expect(calls[:write_relationships]).to eq(2)

    calls = flaky(writes, permissions: { write_relationships: write_ok })
    expect { writes.write(SpiceDB::Transaction.new.create(viewer)) }.to raise_error(SpiceDB::UnavailableError)
    expect(calls[:write_relationships]).to eq(1)
  end

  it 'rejects a retrying policy for never-idempotent operations when the client is built' do
    SpiceDB::Client::NEVER_IDEMPOTENT_OPERATIONS.each do |operation|
      expect { client_with(operation_retry_policies: { operation => retry_once }) }
        .to raise_error(SpiceDB::InvalidArgumentError, /#{operation} cannot be retried/)
      expect(client_with(operation_retry_policies: { operation => no_retries }).operation_retry_policies).to eq(operation => no_retries)
    end
    expect { client_with(operation_retry_policies: { wach: no_retries }) }.to raise_error(SpiceDB::InvalidArgumentError, /unknown operation :wach/)
    expect { client_with(operation_retry_policies: { watch: 0 }) }.to raise_error(SpiceDB::InvalidArgumentError, /expected SpiceDB::RetryPolicy/)
    expect { client_with(operation_retry_policies: [[:watch, no_retries]]) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'names every operation that takes a retry_policy' do
    with_policy = SpiceDB::Client.public_instance_methods.select do |name|
      SpiceDB::Client.instance_method(name).parameters.include?(%i[key retry_policy])
    end
    expect(SpiceDB::Client::OPERATIONS).to match_array(with_policy)
  end
end
