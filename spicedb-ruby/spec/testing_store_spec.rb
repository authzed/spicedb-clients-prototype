# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require 'grpc'

RSpec.describe SpiceDB::Testing::Store do
  let(:store) { described_class.new }
  let(:full) { SpiceDB::Consistency.full }

  def rel(tuple) = SpiceDB::Relationship.from_tuple(tuple)

  it 'has the client surface for every operation it implements' do
    %i[write read_relationships delete_relationships import_relationships export_relationships read_schema write_schema reflect_schema
       check check_bulk].each do |name|
      expect(described_class.instance_method(name).parameters).to eq(SpiceDB::Client.instance_method(name).parameters), name.to_s
    end
  end

  it 'raises server errors shaped like the client: code, server message as message' do
    store.write(SpiceDB::Transaction.new.touch(rel('org:acme#admin@user:alice')))
    owner = SpiceDB::Filter.new(resource_type: 'org', relation: 'owner')
    expect { store.write(SpiceDB::Transaction.new.must_match(owner)) }.to raise_error(SpiceDB::FailedPreconditionError) do |error|
      expect(error.message).to start_with('unable to satisfy write precondition `')
      expect(error.server_message).to eq(error.message)
      expect(error.code).to eq(GRPC::Core::StatusCodes::FAILED_PRECONDITION)
      expect(error.reason).to eq('ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE')
    end
    expect { store.write(SpiceDB::Transaction.new.create(rel('org:acme#admin@user:alice'))) }.to raise_error(SpiceDB::AlreadyExistsError) do |error|
      expect(error.server_message).to start_with('could not CREATE relationship `org:acme#admin@user:alice`, as it already existed')
    end
  end

  it 'raises local validation like the client: InvalidArgumentError whose server message is its own message' do
    expect { store.read_relationships(SpiceDB::Filter.new(resource_type: 'org'), consistency: full, limit: 0) }
      .to raise_error(SpiceDB::InvalidArgumentError) { |error| expect(error.server_message).to eq('limit must be a positive integer') }
    expect { store.delete_relationships(SpiceDB::Filter.new(resource_type: 'org')) }.to raise_error(SpiceDB::InvalidArgumentError, /limit/)
    retry_once = SpiceDB::RetryPolicy.new(max_retries: 1)
    expect { store.write(SpiceDB::Transaction.new.create(rel('org:acme#admin@user:alice')), retry_policy: retry_once) }
      .to raise_error(SpiceDB::InvalidArgumentError, /cannot be retried/)
    expect(store.write(SpiceDB::Transaction.new.touch(rel('org:acme#admin@user:alice')), retry_policy: retry_once).revision).not_to be_nil
  end

  it 'checks preconditions on empty writes and returns the current revision' do
    first = store.write(SpiceDB::Transaction.new.touch(rel('org:acme#admin@user:alice'))).revision
    expect(store.write(SpiceDB::Transaction.new).revision).to eq(first)
    expect { store.write(SpiceDB::Transaction.new.must_not_match(SpiceDB::Filter.new(resource_type: 'org'))) }
      .to raise_error(SpiceDB::FailedPreconditionError)
  end

  it 'deletes all or nothing like SpiceDB' do
    store.import_relationships([rel('doc:a#viewer@user:x'), rel('doc:b#viewer@user:x')])
    expect { store.delete_relationships(SpiceDB::Filter.new(resource_type: 'doc'), limit: 1, partial: SpiceDB::DeletePartial::DISALLOWED) }
      .to raise_error(SpiceDB::InvalidArgumentError) { |error| expect(error.server_message).to include('partial deletion was not requested') }
    expect(store.relationships.length).to eq(2)
  end

  it 'imports in one transaction, and reads and exports with cursors, limits and filters' do
    tuples = %w[doc:a#viewer@user:x doc:b#viewer@user:x doc:c#viewer@user:x folder:f#viewer@user:x]
    expect(store.import_relationships(tuples.map { |tuple| rel(tuple) })).to eq(4)
    expect { store.import_relationships([rel('doc:z#viewer@user:x'), rel('doc:a#viewer@user:x')]) }.to raise_error(SpiceDB::AlreadyExistsError)
    expect(store.relationships.length).to eq(4), 'a failed import adds nothing'

    docs = SpiceDB::Filter.new(resource_type: 'doc')
    first = store.read_relationships(docs, consistency: full, limit: 2)
    expect(first.map(&:resource_id)).to eq(%w[a b])
    expect(store.read_relationships(docs, consistency: full, cursor: first.to_a.last.cursor).map(&:resource_id)).to eq(%w[c])
    expect { store.read_relationships(docs, consistency: full, cursor: 'bogus') }.to raise_error(SpiceDB::InvalidArgumentError) do |error|
      expect(error.server_message).to start_with('error decoding cursor')
    end

    exported = store.export_relationships(SpiceDB::Filter.new(resource_type: 'folder'), consistency: full)
    expect(exported.map(&:to_s)).to eq(%w[folder:f#viewer@user:x])
    expect(exported.first).to be_a(SpiceDB::ExportedRelationship)
    all = []
    store.export_relationships(consistency: full, limit: 3) { |item| all << item.to_s }
    expect(all.length).to eq(3)
  end

  it 'reads, writes and reflects schema' do
    expect { store.read_schema }.to raise_error(SpiceDB::NotFoundError) { |error| expect(error.server_message).to start_with('no schema has been defined') }
    expect(store.reflect_schema(consistency: full).definitions).to eq([])
    revision = store.write_schema('definition user {}')
    expect(revision).to be_a(SpiceDB::ZedToken)
    expect(store.read_schema.schema).to eq('definition user {}')
    definition = SpiceDB::SchemaDefinition.new(name: 'user', comment: '', relations: [], permissions: [])
    store.set_reflect_schema_result(SpiceDB::ReflectSchemaResult.new(definitions: [definition], caveats: [], revision: nil))
    reflected = store.reflect_schema(consistency: SpiceDB::Consistency.min_latency)
    expect(reflected.definitions.map(&:name)).to eq(['user'])
    expect(reflected.revision).to eq(revision)
  end

  it 'fills response metadata, also on failure' do
    store.set_response_metadata(header: { 'io.spicedb.respmeta.version' => 'v1.99' }, trailer: { 't' => '1' })
    capture = SpiceDB::ResponseMetadata.new
    expect { store.read_schema(call_options: SpiceDB::CallOptions.new(response_metadata: capture)) }.to raise_error(SpiceDB::NotFoundError)
    expect(capture.header).to eq('io.spicedb.respmeta.version' => 'v1.99')
    expect(capture.trailer).to eq('t' => '1')
  end
end
