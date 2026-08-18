# frozen_string_literal: true

require_relative '../spec_helper'

# Demonstrates the client-level `default_timeout:` construction parameter, a
# per-call `timeout:` override on a unary call, and that bulk import
# (`import_relationships`) is a client-streaming call that is NOT bounded by
# `default_timeout` -- see root DESIGN.md, "RULE: A unary call must have a
# deadline".
RSpec.describe 'Call deadlines' do
  it 'accepts default_timeout: on the real client construction path' do
    # default_timeout: applies to every unary call that doesn't pass its own
    # timeout: override. This is the documented, real construction path --
    # not a mock -- so a signature drift here (e.g. the keyword silently
    # disappearing from Client.new_plaintext) would fail this example, not
    # just a unit spec against a stalling stub.
    SpiceDB::Client.new_plaintext(SPICEDB_ENDPOINT, SPICEDB_TOKEN, default_timeout: 5) do |c|
      c.write_schema(TEST_SCHEMA)
      c.delete_relationships(SpiceDB::Filter.new(resource_type: 'document'))

      txn = SpiceDB::Transaction.new
      txn.touch(SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice'))
      c.write(txn)

      rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
      result = c.check_permission(SpiceDB::Consistency.full, 'view', rel)
      expect(result.has_permission?).to be true

      c.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('readme'))
    end
  end

  it 'lets a per-call timeout: override the client default' do
    # 5 seconds is generous for a real call against a local SpiceDB -- this
    # exercises the real timeout: keyword end-to-end, not testing how small
    # a timeout can be.
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'readme', 'viewer', 'user', 'alice'))
    client.write(txn, timeout: 5)

    rel = SpiceDB::Relationship.from_triple('document', 'readme', 'view', 'user', 'alice')
    result = client.check_permission(SpiceDB::Consistency.full, 'view', rel, timeout: 5)
    expect(result.has_permission?).to be true
  ensure
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('readme'))
  end

  it 'does not bound bulk import by the unary default' do
    # import_relationships (import_bulk_relationships) is client-streaming:
    # its duration scales with the size of the caller's dataset, not with
    # server latency, so it is explicitly excluded from default_timeout.
    # Calling it with no timeout: at all must still succeed.
    users = (1..50).map { |i| "user#{i}" }
    relationships = users.map do |u|
      SpiceDB::Relationship.from_triple('document', 'bulk', 'viewer', 'user', u)
    end
    num_loaded = client.import_relationships(relationships)
    expect(num_loaded).to eq(users.length)

    # A caller-supplied timeout: on the same client-streaming call must
    # still be honored -- the exclusion is from the *default*, not from the
    # ability to bound the call at all.
    more_relationships = users.map do |u|
      SpiceDB::Relationship.from_triple('document', 'bulk2', 'viewer', 'user', u)
    end
    num_loaded_bounded = client.import_relationships(more_relationships, timeout: 30)
    expect(num_loaded_bounded).to eq(users.length)
  ensure
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('bulk'))
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'document').with_resource_id('bulk2'))
  end
end
