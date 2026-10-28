# frozen_string_literal: true

require_relative '../lib/spicedb'
require_relative '../lib/spicedb/testing'
require 'grpc'

# Call recording and fault injection (root DESIGN.md "Make native values
# observable and testable").
RSpec.describe SpiceDB::Testing::Store do
  let(:store) { described_class.new }
  let(:full) { SpiceDB::Consistency.full }
  let(:codes) { GRPC::Core::StatusCodes }
  let(:stage) { SpiceDB::Testing::FaultStage }

  def tuples(count) = (0...count).map { |index| SpiceDB::Relationship.from_tuple("doc:d#{index}#viewer@user:x") }

  it 'fails import batch N with ALREADY_EXISTS and applies nothing' do
    seen = []
    store.fault_hook = lambda do |fault|
      seen << [fault.stage, fault.item] if fault.call.operation == :import_relationships
      next unless fault.stage == stage::IMPORT_BATCH && fault.item == 2

      described_class.server_error(codes::ALREADY_EXISTS, 'duplicate relationship')
    end
    expect { store.import_relationships(tuples(10), batch_size: 3) }.to raise_error(SpiceDB::AlreadyExistsError) do |error|
      expect(error.message).to eq('duplicate relationship')
      expect(error.server_message).to eq('duplicate relationship')
    end
    expect(store.relationships).to be_empty
    expect(seen).to eq([[stage::CALL, nil], [stage::IMPORT_BATCH, 0], [stage::IMPORT_BATCH, 1], [stage::IMPORT_BATCH, 2]])
  end

  it 'fails an import at commit, applying nothing' do
    store.fault_hook = ->(fault) { described_class.server_error(codes::UNAVAILABLE, 'lost') if fault.stage == stage::IMPORT_COMMIT }
    expect { store.import_relationships(tuples(4)) }.to raise_error(SpiceDB::UnavailableError)
    expect(store.relationships).to be_empty
  end

  it 'fails an export after K items, with or without a block' do
    store.import_relationships(tuples(5))
    store.fault_hook = lambda do |fault|
      described_class.server_error(codes::UNAVAILABLE, 'stream reset') if fault.stage == stage::ITEM && fault.item == 3
    end
    got = []
    expect { store.export_relationships(consistency: full) { |item| got << item.to_s } }.to raise_error(SpiceDB::UnavailableError)
    expect(got).to eq(%w[doc:d0#viewer@user:x doc:d1#viewer@user:x doc:d2#viewer@user:x])

    stream = store.export_relationships(consistency: full)
    expect(stream.first(3).length).to eq(3)
    expect { stream.to_a }.to raise_error(SpiceDB::UnavailableError, 'stream reset')
    expect { store.read_relationships(SpiceDB::Filter.new(resource_type: 'doc'), consistency: full).to_a }.to raise_error(SpiceDB::UnavailableError)
  end

  it 'fails exactly one call with fail_next' do
    check = SpiceDB::PermissionCheck.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'd0'), permission: 'view',
                                         subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'x')))
    store.fail_next(:check, described_class.server_error(codes::UNAVAILABLE, 'down'))
    store.write(SpiceDB::Transaction.new.touch(tuples(1).first))
    expect { store.check(check, consistency: full) }.to raise_error(SpiceDB::UnavailableError, 'down')
    expect(store.check(check, consistency: full).permissionship).to eq(:no_permission)
    expect { store.fail_next(:chek, StandardError.new) }.to raise_error(SpiceDB::InvalidArgumentError)
  end

  it 'applies nothing when a write fails at its call' do
    store.fail_next(:write, described_class.server_error(codes::UNAVAILABLE, 'down'))
    expect { store.write(SpiceDB::Transaction.new.touch(tuples(1).first)) }.to raise_error(SpiceDB::UnavailableError)
    expect(store.relationships).to be_empty
  end

  it 'records every call with its request and keyword options' do
    docs = SpiceDB::Filter.new(resource_type: 'doc')
    folders = SpiceDB::Filter.new(resource_type: 'folder')
    store.read_relationships(docs, consistency: full, cursor: '2', limit: 5).to_a
    store.read_relationships(folders, consistency: full) { |_| nil }
    store.read_schema rescue SpiceDB::NotFoundError # rubocop:disable Style/RescueModifier

    reads = store.calls_of(:read_relationships)
    expect(reads.map(&:index)).to eq([0, 1])
    expect(reads[0].options).to include(cursor: '2', limit: 5, page_size: 1000, consistency: full)
    expect(reads[1].request).to eq(folders)
    expect(store.calls.map(&:operation)).to eq(%i[read_relationships read_relationships read_schema])
    expect(store.calls.last.request).to be_nil
    expect(store.calls).to be_frozen
  end

  it 'rejects a fault hook that is not callable' do
    expect { store.fault_hook = 3 }.to raise_error(SpiceDB::InvalidArgumentError)
    store.fault_hook = nil
  end
end
