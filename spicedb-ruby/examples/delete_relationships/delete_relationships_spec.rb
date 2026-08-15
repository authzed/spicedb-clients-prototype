# frozen_string_literal: true

require_relative '../spec_helper'

# Mirrors spicedb-go's examples/delete_relationships/main.go.
RSpec.describe 'DeleteRelationships' do
  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'bob'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'carol'))
    client.write(txn)
  end

  it 'deletes relationships matching a plain filter (no kwargs, unchanged default behavior)' do
    filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('viewer')

    revision = client.delete_relationships(filter)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it 'guards a delete with must_match:, only removing viewers while the document still has an owner' do
    viewer_filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('viewer')
    owner_guard = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('owner')

    revision = client.delete_relationships(viewer_filter, must_match: [owner_guard])

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it 'rejects the whole delete (deleting nothing) when a must_match: precondition is not satisfied' do
    owner_filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('owner')
    never_matches = SpiceDB::Filter.new(resource_type: 'document')
                                   .with_resource_id('firstdoc')
                                   .with_relation('viewer')
                                   .with_subject_type('user')
                                   .with_subject_id('nonexistent-subject')

    expect do
      client.delete_relationships(owner_filter, must_match: [never_matches])
    end.to raise_error(SpiceDB::FailedPreconditionError)
  end

  it 'overrides the default 10,000-per-call page size with limit:' do
    owner_filter = SpiceDB::Filter.new(resource_type: 'document').with_resource_id('firstdoc').with_relation('owner')

    revision = client.delete_relationships(owner_filter, limit: 1)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end
end
