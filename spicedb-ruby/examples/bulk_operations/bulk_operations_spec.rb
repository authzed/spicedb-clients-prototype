# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'BulkOperations' do
  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    %w[alice bob charlie].each do |user|
      txn.touch(SpiceDB::Relationship.from_triple('document', 'report', 'viewer', 'user', user))
    end
    @revision = client.write(txn)
  end

  it 'bulk checks permissions for multiple relationships' do
    checks = %w[alice bob charlie].map do |user|
      SpiceDB::Relationship.from_triple('document', 'report', 'viewer', 'user', user)
    end

    results = client.check_permissions(
      SpiceDB::Consistency.at_least(@revision),
      'view',
      *checks
    )

    expect(results.length).to eq(3)
    expect(results).to all(be true)
  end

  it 'check_all returns true when all subjects have permission' do
    checks = %w[alice bob charlie].map do |user|
      SpiceDB::Relationship.from_triple('document', 'report', 'viewer', 'user', user)
    end

    result = client.check_all(
      SpiceDB::Consistency.at_least(@revision),
      'view',
      *checks
    )

    expect(result).to be true
  end

  it 'check_any returns true when at least one subject has permission' do
    checks = [
      SpiceDB::Relationship.from_triple('document', 'report', 'viewer', 'user', 'alice'),
      SpiceDB::Relationship.from_triple('document', 'report', 'viewer', 'user', 'nobody')
    ]

    result = client.check_any(
      SpiceDB::Consistency.at_least(@revision),
      'view',
      *checks
    )

    expect(result).to be true
  end

  it 'imports relationships in bulk' do
    relationships = (1..50).map do |i|
      SpiceDB::Relationship.from_triple('document', "bulk-#{i}", 'viewer', 'user', 'alice')
    end

    count = client.import_relationships(relationships)

    expect(count).to eq(50)
  end

  it 'exports relationships in bulk' do
    relationships = client.export_relationships(
      SpiceDB::Consistency.at_least(@revision),
      SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer')
    ).to_a

    expect(relationships.length).to be >= 3
    expect(relationships.first).to be_a(SpiceDB::Relationship)
  end
end
