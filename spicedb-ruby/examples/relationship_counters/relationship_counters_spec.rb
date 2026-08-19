# frozen_string_literal: true

require_relative '../spec_helper'

# Bounds how long a counter may stay "still calculating" before these examples
# fail. Expiry is a failure, deliberately, and not a way out of asserting.
COUNTER_TIMEOUT = 30
COUNTER_POLL_INTERVAL = 0.1

# Polls `experimental_count_relationships` until the named counter settles.
#
# The alternative -- sleep a fixed two seconds and then wrap every assertion in
# `unless result.still_calculating` -- asserts nothing at all on a slow run, and
# nothing on ANY run if the still_calculating mapping is inverted, which is the
# likeliest bug on that exact field. Coverage that comes and goes between runs,
# both of them green, is not coverage.
def settled_counter(client, counter_name)
  deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + COUNTER_TIMEOUT
  loop do
    result = client.experimental_count_relationships(counter_name)
    return result unless result.still_calculating

    raise "counter #{counter_name} never settled within #{COUNTER_TIMEOUT}s" if Process.clock_gettime(Process::CLOCK_MONOTONIC) > deadline

    sleep(COUNTER_POLL_INTERVAL)
  end
end

RSpec.describe 'RelationshipCounters' do
  before do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'viewer', 'user', 'bob'))
    # An `editor` the counter's filter must NOT count. Without a relationship
    # the filter has to exclude, a counter that ignored the relation filter
    # entirely would still report the expected number.
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'editor', 'user', 'carol'))
    client.write(txn)
  end

  it 'registers, reads, and unregisters a relationship counter' do
    counter_name = "document_viewers_#{rand(100_000)}"
    filter = SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer')

    # Register the counter
    client.experimental_register_relationship_counter(counter_name, filter)

    result = settled_counter(client, counter_name)

    expect(result).to be_a(SpiceDB::CountResult)
    expect(result.still_calculating).to be false
    expect(result.revision).not_to be_empty

    # Exactly the two viewer relationships written above, and not the editor.
    # A count of zero -- registration silently no-op'ing, or the value never
    # being read off the response -- fails here, and so does a count of three,
    # which is what ignoring the relation filter would produce. The shared hook
    # clears `document` before each example, so this number is this example's
    # own writes and not an earlier example's leftovers.
    expect(result.relationship_count).to eq(2)

    # Unregister the counter
    client.experimental_unregister_relationship_counter(counter_name)

    # Unregistering has to actually remove it: reading a counter that is not
    # registered is an error, so a no-op unregister would leave this succeeding.
    expect { client.experimental_count_relationships(counter_name) }
      .to raise_error(SpiceDB::FailedPreconditionError)
  end

  it 'reports a counter that has settled as no longer calculating' do
    counter_name = "fresh_counter_#{rand(100_000)}"
    filter = SpiceDB::Filter.new(resource_type: 'document').with_relation('viewer')

    client.experimental_register_relationship_counter(counter_name, filter)

    # `expect([true, false]).to include(result.still_calculating)` used to
    # stand here, which passes for anything except nil -- it cannot fail on a
    # counter that never settles, on an inverted mapping, or on a count that is
    # always zero. A settled counter has still_calculating false and a
    # non-empty revision, and that is what is asserted.
    result = settled_counter(client, counter_name)

    expect(result).to be_a(SpiceDB::CountResult)
    expect(result.still_calculating).to be false
    expect(result.revision).not_to be_empty
    expect(result.relationship_count).to eq(2)

    # Cleanup
    client.experimental_unregister_relationship_counter(counter_name)
  end
end
