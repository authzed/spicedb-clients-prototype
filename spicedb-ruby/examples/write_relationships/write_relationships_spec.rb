# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'WriteRelationships' do
  it 'writes relationships with touch and returns a native result' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'editor',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob'), relation: nil)))

    result = client.write(txn)

    expect(result).to be_a(SpiceDB::WriteResult)
    expect(result.revision).to be_a(SpiceDB::ZedToken)
    expect(result.revision.to_s).not_to be_empty
  end

  it 'writes relationships with create operation' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.create(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'newdoc'), relation: 'owner',
                                         subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                         object_id: 'charlie'), relation: nil)))

    revision = client.write(txn).revision

    expect(revision).not_to be_nil
    expect(revision.to_s).not_to be_empty
  end

  it 'supports preconditions with must_not_match' do
    client.write_schema(TEST_SCHEMA)

    # Ensure mallory is not already an owner before writing
    precondition_filter = SpiceDB::Filter.new(resource_type: 'document')
                                         .with_resource_id('firstdoc')
                                         .with_relation('owner')
                                         .with_subject(SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: 'mallory'))

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.must_not_match(precondition_filter)

    revision = client.write(txn).revision

    expect(revision).not_to be_nil
    expect(revision.to_s).not_to be_empty
  end

  it 'surfaces the reason and its metadata when a precondition fails' do
    # A failed precondition arrives with SpiceDB's structured explanation
    # attached, not just a message: the typed error carries the
    # authzed.api.v1.ErrorReason name and the metadata naming which
    # precondition did not hold, so recovery can be written against data rather
    # than against a parsed string.
    client.write_schema(TEST_SCHEMA)

    unsatisfiable = SpiceDB::Filter.new(resource_type: 'document')
                                   .with_resource_id('firstdoc')
                                   .with_relation('owner')
                                   .with_subject(SpiceDB::SubjectFilter.new(subject_type: 'user', subject_id: 'nobody'))

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'seconddoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.must_match(unsatisfiable)

    error = nil
    begin
      client.write(txn)
    rescue SpiceDB::FailedPreconditionError => e
      error = e
    end

    expect(error).not_to be_nil
    expect(error.reason).to eq('ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE')
    expect(error.reason_domain).to eq('authzed.com')
    expect(error.reason_metadata['precondition_resource_id']).to eq('firstdoc')
  end

  it 'supports delete operations' do
    client.write_schema(TEST_SCHEMA)

    # First, create a relationship
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    # Then, delete it
    txn = SpiceDB::Transaction.new
    txn.delete(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                         subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                         object_id: 'alice'), relation: nil)))

    revision = client.write(txn).revision

    expect(revision).not_to be_nil
    expect(revision.to_s).not_to be_empty
  end
end
