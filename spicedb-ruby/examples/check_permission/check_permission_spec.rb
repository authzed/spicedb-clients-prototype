# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'CheckPermission' do
  it 'checks a single permission and reports HAS_PERMISSION when granted' do
    # Setup: write schema and test data
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    client.write(txn)

    # Check permission — alice is a viewer, so she can view.
    #
    # check_permission returns a SpiceDB::CheckResult, not a bare Boolean —
    # use #has_permission? rather than testing the result itself. Ruby has
    # no way to make an object falsy (no `__bool__` hook), so `if result`
    # would be unconditionally true even for a conditional (unevaluated)
    # grant; only #has_permission? tells you whether it's actually allowed.
    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice')
    result = client.check_permission(SpiceDB::Consistency.full, 'view', rel)

    expect(result).to be_a(SpiceDB::CheckResult)
    expect(result.has_permission?).to be true
    expect(result.permissionship).to eq(:has_permission)
    expect(result.checked_at).not_to be_empty
  end

  it 'reports NO_PERMISSION (has_permission? false) when permission is not granted' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    client.write(txn)

    # alice is only a viewer, she cannot delete
    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice')
    result = client.check_permission(SpiceDB::Consistency.full, 'delete', rel)

    expect(result.has_permission?).to be false
    expect(result.permissionship).to eq(:no_permission)
  end

  it 'uses at_least consistency with a revision from a write' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice'))
    revision = client.write(txn)

    rel = SpiceDB::Relationship.from_triple('document', 'firstdoc', 'owner', 'user', 'alice')
    result = client.check_permission(SpiceDB::Consistency.at_least(revision), 'delete', rel)

    expect(result.has_permission?).to be true
  end

  it 'reports CONDITIONAL_PERMISSION (has_permission? false) when caveat context is not supplied' do
    # A caveated relationship whose caveat context isn't supplied at check
    # time is exactly where the server says "I need more information" —
    # that's neither a grant nor a denial, and collapsing it to a Boolean
    # (as older client generations did) silently turns "you forgot to pass
    # context" into either an over-grant or an over-deny.
    client.write_schema(<<~SCHEMA)
      caveat active(now int) { now < 100 }
      definition user {}
      definition doc {
      	relation viewer: user with active
      	permission view = viewer
      }
    SCHEMA

    txn = SpiceDB::Transaction.new
    txn.touch(
      SpiceDB::Relationship.from_triple('doc', 'conditionaldoc', 'viewer', 'user', 'alice')
                            .with_caveat('active', {})
    )
    client.write(txn)

    rel = SpiceDB::Relationship.from_triple('doc', 'conditionaldoc', 'viewer', 'user', 'alice')
    # No caveat context is supplied here — SpiceDB can't evaluate `active`.
    result = client.check_permission(SpiceDB::Consistency.full, 'view', rel)

    expect(result.permissionship).to eq(:conditional_permission)
    expect(result.missing_context).to include('now')
    expect(result.has_permission?).to be false
  ensure
    # Clean up so the next example's around-hook can restore TEST_SCHEMA
    # (which doesn't have a `doc` definition or `active` caveat) without
    # SpiceDB rejecting it for a dangling relationship/caveat reference.
    client.delete_relationships(SpiceDB::Filter.new(resource_type: 'doc'))
  end
end
