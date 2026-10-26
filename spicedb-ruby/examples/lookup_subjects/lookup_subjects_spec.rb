# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'LookupSubjects' do
  it 'finds all subjects with access to a resource' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'editor',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob'), relation: nil)))
    client.write(txn)

    # Both alice (viewer) and bob (editor) can view
    results = client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), permission: 'view',
                                     subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a

    # Keep wildcard matches and their exclusions out of a concrete access list.
    subject_ids = results.filter_map do |r|
      next unless r.subject.permissionship == :has_permission && r.subject.subject_id != '*' && r.excluded_subjects.empty?

      r.subject.subject_id
    end
    expect(subject_ids).to include('alice')
    expect(subject_ids).to include('bob')

    # Each result is a native SpiceDB::LookupSubject wrapping a resolved
    # subject — not a bare ID. Results also aren't guaranteed unique (the
    # same subject can be yielded more than once), which is why this
    # asserts with `include` rather than an exact-array match.
    expect(results).to all(be_a(SpiceDB::LookupSubject))
    expect(results.map { |r| r.subject.permissionship }).to all(eq(:has_permission))
  end

  it 'returns only subjects with the specific permission' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'owner',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob'), relation: nil)))
    client.write(txn)

    # Only bob (owner) can delete
    subject_ids = client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), permission: 'delete',
                                         subject_type: 'user', consistency: SpiceDB::Consistency.full).filter_map do |r|
      next unless r.subject.permissionship == :has_permission && r.subject.subject_id != '*' && r.excluded_subjects.empty?

      r.subject.subject_id
    end

    expect(subject_ids).to include('bob')
    expect(subject_ids).not_to include('alice')
  end

  it 'returns empty when no subjects have access' do
    client.write_schema(TEST_SCHEMA)

    results = client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'nonexistent'), permission: 'view',
                                     subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a

    expect(results).to be_empty
  end

  it 'surfaces excluded_subjects for a wildcard "*" grant — the over-grant security fix' do
    # `banned` carves subjects back out of the public/wildcard viewer grant.
    # A caller that only looked at the wildcard subject ID ("*") without
    # checking excluded_subjects would incorrectly treat `eve` as granted.
    client.write_schema(<<~SCHEMA)
      definition user {}

      definition document {
        relation viewer: user | user:*
        relation banned: user
        permission view = viewer - banned
      }
    SCHEMA

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'public_doc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: '*'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'public_doc'), relation: 'banned',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'eve'), relation: nil)))
    client.write(txn)

    results = client.lookup_subjects(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'public_doc'), permission: 'view',
                                     subject_type: 'user', consistency: SpiceDB::Consistency.full).to_a

    wildcard_result = results.find { |r| r.subject.subject_id == '*' }
    expect(wildcard_result).not_to be_nil

    excluded_ids = wildcard_result.excluded_subjects.map(&:subject_id)
    expect(excluded_ids).to include('eve')
    expect(wildcard_result.excluded_subjects).to all(be_a(SpiceDB::ResolvedSubject))
  ensure
    # Clean up the wildcard/banned relationships so the next example's
    # around-hook can restore TEST_SCHEMA (which drops the `viewer: user:*`
    # and `banned` relation types) without SpiceDB rejecting it for a
    # dangling relationship reference.
    client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'document'))
  end
end
