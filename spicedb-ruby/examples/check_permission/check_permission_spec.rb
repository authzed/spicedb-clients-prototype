# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'CheckPermission' do
  it 'checks a single permission and reports HAS_PERMISSION when granted' do
    # Setup: write schema and test data
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    # Check permission — alice is a viewer, so she can view.
    #
    # check_permission returns a SpiceDB::CheckResult, not a bare Boolean —
    # use #has_permission? rather than testing the result itself. Ruby has
    # no way to make an object falsy (no `__bool__` hook), so `if result`
    # would be unconditionally true even for a conditional (unevaluated)
    # grant; only #has_permission? tells you whether it's actually allowed.
    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    result = client.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject),
                          consistency: SpiceDB::Consistency.full, tracing: SpiceDB::Tracing::ENABLED)

    expect(result).to be_a(SpiceDB::CheckResult)
    expect(result.has_permission?).to be true
    expect(result.permissionship).to eq(:has_permission)
    expect(result.checked_at.to_s).not_to be_empty
    expect(result.debug_trace.schema_used).to include('definition document')
    expect(result.debug_trace.check).not_to be_nil
  end

  it 'reports NO_PERMISSION (has_permission? false) when permission is not granted' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    client.write(txn)

    # alice is only a viewer, she cannot delete
    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    result = client.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'delete', subject: rel.subject),
                          consistency: SpiceDB::Consistency.full)

    expect(result.has_permission?).to be false
    expect(result.permissionship).to eq(:no_permission)
  end

  it 'uses at_least consistency with a revision from a write' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'owner',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    revision = client.write(txn).revision

    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'owner',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    result = client.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'delete', subject: rel.subject),
                          consistency: SpiceDB::Consistency.at_least(revision))

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
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'conditionaldoc'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
                            .with_caveat('active', {})
    )
    client.write(txn)

    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'conditionaldoc'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    # No caveat context is supplied here — SpiceDB can't evaluate `active`.
    result = client.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject),
                          consistency: SpiceDB::Consistency.full)

    expect(result.permissionship).to eq(:conditional_permission)
    expect(result.missing_context).to include('now')
    expect(result.has_permission?).to be false
  ensure
    # Clean up so the next example's around-hook can restore TEST_SCHEMA
    # (which doesn't have a `doc` definition or `active` caveat) without
    # SpiceDB rejecting it for a dangling relationship/caveat reference.
    client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'doc'))
  end

  # C5 (integration/payoff test): the whole point of `missing_context` is
  # that a caller can act on it -- supply the context it names and get a
  # real grant back, not just a different flavor of "I don't know." Same
  # caveat schema as the CONDITIONAL_PERMISSION test above (`active(now
  # int) { now < 100 }`); this time `'now' => 42` (which satisfies `now < 100`)
  # is supplied at check time via the call-level `context:` keyword, and
  # the conditional resolves to an actual :has_permission grant.
  it 'resolves a CONDITIONAL_PERMISSION into a grant when the missing caveat context is supplied' do
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
      SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'conditionaldoc'), relation: 'viewer',
                                subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
                            .with_caveat('active', {})
    )
    client.write(txn)

    rel = SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'doc', object_id: 'conditionaldoc'), relation: 'viewer',
                                    subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'alice'), relation: nil))
    # `'now' => 42` satisfies `now < 100` -- supplying it at check time is what
    # turns the earlier CONDITIONAL_PERMISSION into a real grant.
    result = client.check(SpiceDB::PermissionCheck.new(resource: rel.resource, permission: 'view', subject: rel.subject),
                          consistency: SpiceDB::Consistency.full, context: { 'now' => 42 })

    expect(result.permissionship).to eq(:has_permission)
    expect(result.has_permission?).to be true
    expect(result.missing_context).to eq([])
  ensure
    client.delete_all_relationships_non_atomic(SpiceDB::Filter.new(resource_type: 'doc'))
  end
end
