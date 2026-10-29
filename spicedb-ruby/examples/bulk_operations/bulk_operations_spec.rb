# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'BulkOperations' do
  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    %w[alice bob charlie].each do |user|
      txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'report'), relation: 'viewer',
                                          subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                          object_id: user), relation: nil)))
    end
    @revision = client.write(txn).revision
  end

  it 'bulk checks permissions for multiple relationships' do
    checks = %w[alice bob charlie].map do |user|
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'report'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: user), relation: nil))
    end

    # Inputs over 1,000 relationships are split into one
    # CheckBulkPermissions request per 1,000 automatically and the results
    # concatenated in input order -- SpiceDB rejects a single request
    # carrying more than 10,000. Nothing here changes for a larger Array.
    #
    # check_permissions returns an Array<SpiceDB::CheckResult>, not
    # booleans — use #has_permission? per result rather than testing a
    # result itself (every CheckResult is truthy in Ruby regardless of
    # permissionship, since Ruby has no `__bool__`-style override).
    results = client.check_bulk([*checks].flatten.map do |rel|
      SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject)
    end, consistency: SpiceDB::Consistency.at_least(@revision))

    expect(results.length).to eq(3)
    expect(results).to all(be_a(SpiceDB::CheckResult))
    expect(results.map(&:has_permission?)).to all(be true)
  end

  it 'check_all returns true when all subjects have permission' do
    checks = %w[alice bob charlie].map do |user|
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'report'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: user), relation: nil))
    end

    result = client.check_bulk([*checks].flatten.map do |rel|
      SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject)
    end, consistency: SpiceDB::Consistency.at_least(@revision)).all?(&:has_permission?)

    expect(result).to be true
  end

  it 'check_any returns true when at least one subject has permission' do
    checks = [
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'report'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil)),
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'report'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'nobody'), relation: nil))
    ]

    result = client.check_bulk([*checks].flatten.map do |rel|
      SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject)
    end, consistency: SpiceDB::Consistency.at_least(@revision)).any?(&:has_permission?)

    expect(result).to be true
  end

  it 'imports relationships in bulk' do
    relationships = (1..50).map do |i|
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: "bulk-#{i}"), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    end

    count = client.import_relationships(relationships)

    expect(count).to eq(50)
  end

  it 'exports relationships in bulk' do
    relationships = client.export_relationships(SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer'),
                                                consistency: SpiceDB::Consistency.at_least(@revision)).to_a

    expect(relationships.length).to be >= 3
    expect(relationships.first).to be_a(SpiceDB::ExportedRelationship)
    expect(relationships.first.relationship).to be_a(SpiceDB::Relationship)
    expect(relationships.first.cursor).not_to be_nil
  end
end
