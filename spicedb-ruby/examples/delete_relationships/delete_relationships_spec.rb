# frozen_string_literal: true

require_relative '../spec_helper'

# Mirrors spicedb-go's examples/delete_relationships/main.go.
#
# Every delete here is read back. `expect(revision).not_to be_empty` was all
# these examples asserted, and a revision comes back whether or not anything
# was deleted -- so `must_match:` and `limit:` being dropped entirely would
# have passed, which is every claim each example's own title makes.
RSpec.describe 'DeleteRelationships' do
  let(:viewer_filter) do
    SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('viewer')
  end
  let(:owner_filter) do
    SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('owner')
  end

  # Reads back how many relationships match, at least as fresh as the revision
  # the delete returned -- which is what makes the read-back a proof rather
  # than a race.
  def count_at(revision, filter)
    client.read_relationships(SpiceDB::Consistency.at_least(revision), filter).count
  end

  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'bob'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'carol'))
    client.write(txn)
  end

  it 'deletes relationships matching a plain filter (no kwargs, unchanged default behavior)' do
    revision = client.delete_relationships(viewer_filter)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty

    # The filter is what decides the blast radius: the viewers go, the owner
    # stays. A delete that ignored the relation filter would take both.
    expect(count_at(revision, viewer_filter)).to eq(0)
    expect(count_at(revision, owner_filter)).to eq(1)
  end

  it 'guards a delete with must_match:, only removing viewers while the document still has an owner' do
    owner_guard = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('owner')

    revision = client.delete_relationships(viewer_filter, must_match: [owner_guard])

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty

    expect(count_at(revision, viewer_filter)).to eq(0)
    expect(count_at(revision, owner_filter)).to eq(1)
  end

  it 'rejects the whole delete (deleting nothing) when a must_match: precondition is not satisfied' do
    never_matches = SpiceDB::Filter.new(resource_type: 'document')
                                   .with_resource_id('firstdoc')
                                   .with_relation('viewer')
                                   .with_subject_type('user')
                                   .with_subject_id('nonexistent-subject')

    expect do
      client.delete_relationships(owner_filter, must_match: [never_matches])
    end.to raise_error(SpiceDB::FailedPreconditionError)

    # "Rejected" has to mean nothing was deleted, which is the whole point of
    # guarding the delete. A server that raised *after* deleting would satisfy
    # the raise_error above.
    expect(client.read_relationships(SpiceDB::Consistency.full, owner_filter).count).to eq(1)
  end

  it 'overrides the default 1,000-per-call page size with limit:' do
    # Two more owners, so a limit of 1 forces three separate server calls. If
    # the auto-paging loop stopped after the first page, the read-back below
    # would still find two owners -- which is what makes `limit: 1` observable
    # at all from the caller's side.
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'dave'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'erin'))
    client.write(txn)
    expect(client.read_relationships(SpiceDB::Consistency.full, owner_filter).count).to eq(3)

    revision = client.delete_relationships(owner_filter, limit: 1)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
    expect(count_at(revision, owner_filter)).to eq(0)
  end
end
