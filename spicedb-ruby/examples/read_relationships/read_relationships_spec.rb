# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'ReadRelationships' do
  it 'reads relationships matching a filter' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob'), relation: nil)))
    client.write(txn)

    # Read all viewer relationships on firstdoc
    filter = SpiceDB::Filter.new(resource_type: 'document')
                            .with_resource_id('firstdoc')
                            .with_relation('viewer')

    relationships = client.read_relationships(filter, consistency: SpiceDB::Consistency.full).to_a

    expect(relationships).not_to be_empty
    expect(relationships.length).to eq(2)

    subject_ids = relationships.map { |relationship| relationship.subject.object.object_id }.sort
    expect(subject_ids).to eq(%w[alice bob])
  end

  it 'returns an empty enumerator when no relationships match' do
    client.write_schema(TEST_SCHEMA)

    filter = SpiceDB::Filter.new(resource_type: 'document')
                            .with_resource_id('nonexistent')
                            .with_relation('viewer')

    relationships = client.read_relationships(filter, consistency: SpiceDB::Consistency.full).to_a

    expect(relationships).to be_empty
  end

  it 'supports filtering by subject type' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    filter = SpiceDB::Filter.new(resource_type: 'document')
                            .with_resource_id('firstdoc')
                            .with_relation('viewer')
                            .with_subject(SpiceDB::SubjectFilter.new(subject_type: 'user'))

    relationships = client.read_relationships(filter, consistency: SpiceDB::Consistency.full).to_a

    expect(relationships.length).to eq(1)
    expect(relationships.first.subject.object.object_id).to eq('alice')
  end
end
