# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'LookupSubjects' do
  it 'finds all subjects with access to a resource' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'editor', 'user', 'bob'))
    client.write(txn)

    # Both alice (viewer) and bob (editor) can view
    results = client.lookup_subjects(
      SpiceDB::Consistency.full,
      'document', 'firstdoc', 'view', 'user'
    ).to_a

    subject_ids = results.map { |r| r.subject.subject_id }
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
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'bob'))
    client.write(txn)

    # Only bob (owner) can delete
    subject_ids = client.lookup_subjects(
      SpiceDB::Consistency.full,
      'document', 'firstdoc', 'delete', 'user'
    ).map { |r| r.subject.subject_id }

    expect(subject_ids).to include('bob')
    expect(subject_ids).not_to include('alice')
  end

  it 'returns empty when no subjects have access' do
    client.write_schema(TEST_SCHEMA)

    results = client.lookup_subjects(
      SpiceDB::Consistency.full,
      'document', 'nonexistent', 'view', 'user'
    ).to_a

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
    txn.touch(SpiceDB::Relationship.from_triple('document', 'public_doc', 'viewer', 'user', '*'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'public_doc', 'banned', 'user', 'eve'))
    client.write(txn)

    results = client.lookup_subjects(
      SpiceDB::Consistency.full,
      'document', 'public_doc', 'view', 'user'
    ).to_a

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
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))
  end
end
