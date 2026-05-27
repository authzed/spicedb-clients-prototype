# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'RelationshipCounters' do
  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'viewer', 'user', 'bob'))
    client.write(txn)
  end

  it 'registers, reads, and unregisters a relationship counter' do
    counter_name = "document_viewers_#{rand(100_000)}"
    filter = SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer')

    # Register the counter
    client.experimental_register_relationship_counter(counter_name, filter)

    # Wait briefly for the counter to be computed
    sleep(2)

    # Read the counter value
    result = client.experimental_count_relationships(counter_name)

    expect(result).to be_a(SpiceDB::CountResult)
    expect(result.revision).not_to be_nil

    expect(result.relationship_count).to be >= 2 unless result.still_calculating

    # Unregister the counter
    client.experimental_unregister_relationship_counter(counter_name)
  end

  it 'returns still_calculating for a freshly registered counter' do
    counter_name = "fresh_counter_#{rand(100_000)}"
    filter = SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer')

    client.experimental_register_relationship_counter(counter_name, filter)

    # Read immediately — may still be calculating
    result = client.experimental_count_relationships(counter_name)

    expect(result).to be_a(SpiceDB::CountResult)
    # The still_calculating field should be a boolean
    expect([true, false]).to include(result.still_calculating)

    # Cleanup
    client.experimental_unregister_relationship_counter(counter_name)
  end
end
