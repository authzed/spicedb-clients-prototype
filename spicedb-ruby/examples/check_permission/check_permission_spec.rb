# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'CheckPermission' do
  it 'checks a single permission and returns true when granted' do
    # Setup: write schema and test data
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    client.write(txn)

    # Check permission — alice is a viewer, so she can view
    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice')
    allowed = client.check_permission(SpiceDB::Consistency.full, 'view', rel)

    expect(allowed).to be true
  end

  it 'returns false when permission is not granted' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    client.write(txn)

    # alice is only a viewer, she cannot delete
    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice')
    allowed = client.check_permission(SpiceDB::Consistency.full, 'delete', rel)

    expect(allowed).to be false
  end

  it 'uses at_least consistency with a revision from a write' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice'))
    revision = client.write(txn)

    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice')
    allowed = client.check_permission(SpiceDB::Consistency.at_least(revision), 'delete', rel)

    expect(allowed).to be true
  end
end
