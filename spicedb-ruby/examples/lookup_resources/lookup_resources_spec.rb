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
    results = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'alice'
    ).to_a

    resource_ids = results.map(&:resource_id)
    expect(resource_ids).to include('firstdoc')
    expect(resource_ids).to include('seconddoc')

    # Each result is a native SpiceDB::LookupResource, not a bare ID — callers
    # MUST check permissionship before treating a result as a full grant.
    expect(results).to all(be_a(SpiceDB::LookupResource))
    expect(results.map(&:permissionship)).to all(eq(:has_permission))
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
    ).map(&:resource_id)

    expect(resource_ids).to include('seconddoc')
    expect(resource_ids).not_to include('firstdoc')
  end

  it 'returns empty when subject has no access' do
    client.write_schema(TEST_SCHEMA)

    results = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'nobody'
    ).to_a

    expect(results).to be_empty
  end

  it 'surfaces a conditional permissionship and missing caveat context for caveated grants' do
    client.write_schema(<<~SCHEMA)
      definition user {}

      caveat has_valid_ip(ip_address string) {
        ip_address == "127.0.0.1"
      }

      definition document {
        relation viewer: user with has_valid_ip
        permission view = viewer
      }
    SCHEMA

    txn = SpiceDB::Transaction.new
    txn.touch(
      SpiceDB::Relationship.from_triple('document', 'caveated', 'viewer', 'user', 'alice')
                            .with_caveat('has_valid_ip', {})
    )
    client.write(txn)

    result = client.lookup_resources(
      SpiceDB::Consistency.full,
      'document', 'view', 'user', 'alice'
    ).find { |r| r.resource_id == 'caveated' }

    # Without caveat context supplied, the server can't fully evaluate the
    # grant — permissionship MUST be checked before treating this as access.
    expect(result.permissionship).to eq(:conditional_permission)
    expect(result.partial_caveat.missing_required_context).to include('ip_address')
  ensure
    # Clean up the caveated relationship so the next example's around-hook
    # can restore TEST_SCHEMA (which drops the `has_valid_ip` caveat type)
    # without SpiceDB rejecting it for a dangling relationship reference.
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))
  end
end
