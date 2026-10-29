# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require 'grpc'

# The double covers the whole client (root DESIGN.md "Make native values
# observable and testable"). Ruby has no static interfaces, so this spec is
# the contract: the Store has every public method of SpiceDB::Client with the
# same parameters.
RSpec.describe SpiceDB::Testing::Store do
  let(:store) { described_class.new(tuples(2)) }
  let(:full) { SpiceDB::Consistency.full }
  let(:codes) { GRPC::Core::StatusCodes }
  let(:stage) { SpiceDB::Testing::FaultStage }

  def tuples(count) = (0...count).map { |index| SpiceDB::Relationship.from_tuple("doc:d#{index}#viewer@user:x") }

  def client_methods = SpiceDB::Client.public_instance_methods - Object.public_instance_methods

  it 'has every public client method with the same parameters' do
    expect(client_methods).not_to be_empty
    client_methods.each do |name|
      expect(described_class.public_method_defined?(name)).to be(true), "Store lacks #{name}"
      expect(described_class.instance_method(name).parameters).to eq(SpiceDB::Client.instance_method(name).parameters), name.to_s
    end
  end

  it 'records every client operation' do
    expect(described_class::OPERATIONS).to eq(SpiceDB::Client::OPERATIONS)
    expect(SpiceDB::Client::OPERATIONS - client_methods).to be_empty
  end

  it 'fails operations without in-memory semantics as UNIMPLEMENTED, after recording them' do
    resource = SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'd0')
    expect { store.expand_permission_tree(resource: resource, permission: 'view', consistency: full) }
      .to raise_error(SpiceDB::Error) do |error|
        expect(error.code).to eq(codes::UNIMPLEMENTED)
        expect(error.message).to eq('expand_permission_tree is not implemented by SpiceDB::Testing::Store')
        expect(error.server_message).to eq(error.message)
      end
    expect { store.experimental_count_relationships('counter') }.to raise_error(SpiceDB::Error) { |error| expect(error.code).to eq(codes::UNIMPLEMENTED) }
    expect(store.calls_of(:experimental_count_relationships).first.request).to eq('counter')

    stream = store.watch
    expect(store.calls_of(:watch).length).to eq(1)
    expect { stream.to_a }.to raise_error(SpiceDB::Error) { |error| expect(error.code).to eq(codes::UNIMPLEMENTED) }
    expect { store.lookup_subjects(resource: resource, permission: 'view', subject_type: 'user', consistency: full) { |_| nil } }
      .to raise_error(SpiceDB::Error) { |error| expect(error.code).to eq(codes::UNIMPLEMENTED) }
  end

  it 'consults the fault hook and validates retry policies for unimplemented operations' do
    store.fail_next(:lookup_resources, described_class.server_error(codes::UNAVAILABLE, 'down'))
    subject_ref = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'x'))
    expect { store.lookup_resources(resource_type: 'doc', permission: 'view', subject: subject_ref, consistency: full) }
      .to raise_error(SpiceDB::UnavailableError, 'down')
    expect(store.calls_of(:lookup_resources).first.options[:resource_type]).to eq('doc')

    filter = SpiceDB::Filter.new(resource_type: 'doc')
    expect { store.experimental_register_relationship_counter('c', filter, retry_policy: SpiceDB::RetryPolicy.new(max_retries: 2)) }
      .to raise_error(SpiceDB::InvalidArgumentError, /cannot be retried/)
  end

  it 'deletes every match with delete_all_relationships_non_atomic and reports progress once' do
    store.write(SpiceDB::Transaction.new.touch(SpiceDB::Relationship.from_tuple('folder:f#viewer@user:x')))
    progress = []
    result = store.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'doc'), on_progress: ->(item) { progress << item })
    expect(result.deleted_count).to eq(2)
    expect(result.progress).to eq(:complete)
    expect(progress).to eq([result])
    expect(store.relationships.map(&:to_s)).to eq(['folder:f#viewer@user:x'])

    expect { store.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'folder'), must_match: [SpiceDB::Filter.new(resource_type: 'doc')]) }
      .to raise_error(SpiceDB::FailedPreconditionError)
    expect(store.relationships.length).to eq(1)
  end

  it 'closes as a no-op and has no protocol client' do
    expect(store.close).to be_nil
    expect(store.proto_client).to be_nil
    expect(store.retry_policy).to be_nil
    expect(store.operation_retry_policies).to eq({})
  end

  it 'can report schema reads without a revision' do
    store.write_schema('definition user {}')
    expect(store.read_schema.revision).not_to be_nil

    store.read_schema_revision = SpiceDB::Testing::SchemaRevision::OMITTED
    omitted = store.read_schema
    expect(omitted.revision).to be_nil
    expect(omitted.schema).to eq('definition user {}')
    expect { store.read_schema_revision = :omitted }.to raise_error(SpiceDB::InvalidArgumentError)

    store.read_schema_revision = SpiceDB::Testing::SchemaRevision::INCLUDED
    expect(store.read_schema.revision).not_to be_nil
  end

  it "passes each import batch's relationships to the fault hook" do
    empty = described_class.new
    batches = []
    empty.fault_hook = lambda do |fault|
      next unless fault.stage == stage::IMPORT_BATCH

      ids = fault.batch.map { |relationship| relationship.resource.object_id }
      next described_class.server_error(codes::ALREADY_EXISTS, 'duplicate d4') if ids.include?('d4')

      batches << ids
      nil
    end
    expect { empty.import_relationships(tuples(7), batch_size: 3) }.to raise_error(SpiceDB::AlreadyExistsError)
    expect(batches).to eq([%w[d0 d1 d2]])
    expect(empty.relationships).to be_empty
  end

  it 'builds resume cursors equal to the store cursor on item n' do
    store = described_class.new(tuples(5))
    cursors = store.export_relationships(consistency: full).map(&:cursor)
    expect(cursors[1]).to eq(described_class.cursor_after(2))
    rest = store.export_relationships(consistency: full, cursor: described_class.cursor_after(3)).map { |item| item.relationship.resource.object_id }
    expect(rest).to eq(%w[d3 d4])
    read = store.read_relationships(SpiceDB::Filter.new(resource_type: 'doc'), consistency: full, cursor: described_class.cursor_after(4)).to_a
    expect(read.map(&:cursor)).to eq([described_class.cursor_after(5)])
    expect { described_class.cursor_after(-1) }.to raise_error(SpiceDB::InvalidArgumentError)
  end
end
