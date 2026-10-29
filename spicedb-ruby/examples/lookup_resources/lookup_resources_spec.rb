# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'LookupResources' do
  it 'finds all resources a subject can access' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'seconddoc'), relation: 'editor',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    # alice can view both documents (viewer implies view, editor implies view)
    results = client.lookup_resources(resource_type: 'document', permission: 'view',
                                      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                      object_id: 'alice')),
                                      consistency: SpiceDB::Consistency.full,
                                      permissionship_filter: SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY).to_a

    resource_ids = results.map(&:resource_id)
    expect(resource_ids).to include('firstdoc')
    expect(resource_ids).to include('seconddoc')

    # Each result is a native SpiceDB::LookupResource, not a bare ID — callers
    # use HAS_PERMISSION_ONLY above to exclude conditional and unknown matches.
    # Results also aren't guaranteed unique (the same resource_id can be
    # yielded more than once), which is why this asserts with `include`
    # rather than an exact-array match.
    expect(results).to all(be_a(SpiceDB::LookupResource))
    expect(results.map(&:permissionship)).to all(eq(:has_permission))
  end

  it 'returns only resources matching the requested permission' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'seconddoc'), relation: 'owner',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    # Only seconddoc should appear for "delete" (owner implies delete)
    resource_ids = client.lookup_resources(resource_type: 'document', permission: 'delete',
                                           subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                           object_id: 'alice')),
                                           consistency: SpiceDB::Consistency.full,
                                           permissionship_filter: SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY).map(&:resource_id)

    expect(resource_ids).to include('seconddoc')
    expect(resource_ids).not_to include('firstdoc')
  end

  it 'returns empty when subject has no access' do
    client.write_schema(TEST_SCHEMA)

    results = client.lookup_resources(resource_type: 'document', permission: 'view',
                                      subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                      object_id: 'nobody')), consistency: SpiceDB::Consistency.full,
                                      permissionship_filter: SpiceDB::LookupPermissionshipFilter::HAS_PERMISSION_ONLY).to_a

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
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'caveated'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
                            .with_caveat('has_valid_ip', {})
    )
    client.write(txn)

    result = client.lookup_resources(resource_type: 'document', permission: 'view',
                                     subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                     object_id: 'alice')),
                                     consistency: SpiceDB::Consistency.full).find do |r|
      r.resource_id == 'caveated'
    end

    # Without caveat context supplied, the server can't fully evaluate the
    # grant — permissionship MUST be checked before treating this as access.
    expect(result.permissionship).to eq(:conditional_permission)
    expect(result.partial_caveat.missing_required_context).to include('ip_address')
  ensure
    # Clean up the caveated relationship so the next example's around-hook
    # can restore TEST_SCHEMA (which drops the `has_valid_ip` caveat type)
    # without SpiceDB rejecting it for a dangling relationship reference.
    client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document'))
  end

  it 'sends one request, like the raw API, in single-request paging mode' do
    client.write_schema(TEST_SCHEMA)
    alice = SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'))
    txn = SpiceDB::Transaction.new
    %w[sr1 sr2 sr3].each do |id|
      txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: id), relation: 'viewer', subject: alice))
    end
    client.write(txn)

    # Without a limit, the one request is unbounded: page_size is ignored, so
    # a page size of 1 still returns every match from a single request, and
    # each item keeps its cursor (the last one is the raw call's final cursor).
    results = client.lookup_resources(resource_type: 'document', permission: 'view', subject: alice, consistency: SpiceDB::Consistency.full,
                                      page_size: 1, paging: SpiceDB::Paging::SINGLE_REQUEST).to_a
    expect(results.map(&:resource_id)).to include('sr1', 'sr2', 'sr3')
    expect(results.map(&:cursor)).to all(be_a(String))

    # With a limit, that limit is the request's limit and no further page follows.
    limited = client.read_relationships(SpiceDB::Filter.new(resource_type: 'document'), consistency: SpiceDB::Consistency.full,
                                                                                        limit: 2, paging: SpiceDB::Paging::SINGLE_REQUEST).to_a
    expect(limited.length).to eq(2)
  ensure
    client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document'))
  end
end
