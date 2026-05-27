# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'LookupResources' do
  it 'finds all resources a subject can access' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'editor', 'user', 'alice'))
    client.write(txn)

    # alice can view both documents (viewer implies view, editor implies view)
    resource_ids = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'alice'
    ).to_a

    expect(resource_ids).to include('firstdoc')
    expect(resource_ids).to include('seconddoc')
  end

  it 'returns only resources matching the requested permission' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'owner', 'user', 'alice'))
    client.write(txn)

    # Only seconddoc should appear for "delete" (owner implies delete)
    resource_ids = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'delete', 'user', 'alice'
    ).to_a

    expect(resource_ids).to include('seconddoc')
    expect(resource_ids).not_to include('firstdoc')
  end

  it 'returns empty when subject has no access' do
    client.write_schema(TEST_SCHEMA)

    resource_ids = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'nobody'
    ).to_a

    expect(resource_ids).to be_empty
  end
end
