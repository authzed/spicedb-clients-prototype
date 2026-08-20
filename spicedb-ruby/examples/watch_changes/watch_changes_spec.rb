# frozen_string_literal: true

require 'timeout'

require_relative '../spec_helper'

# Watch is an open-ended server stream: it never completes on its own. A
# consumer that just iterates it cannot end, and one that ends on a timeout
# without asserting cannot fail. So both examples here are *bounded* consumers:
# subscribe from a known revision, make a write that must produce a specific
# update, consume until exactly that update has been observed, then `break`.
#
# `break` is this client's abandonment path, and it is what releases the
# stream. Ruby's internal iteration runs the Enumerator's block on the caller's
# own fiber, so the unwind reaches `GRPC::ActiveCall#each_remote_read_then_finish`,
# whose `ensure` closes the core call synchronously. That chain, and the reason
# there is deliberately no explicit `cancel` to call instead, is written up in
# ../../DESIGN.md, "Stream lifecycle: abandoning an Enumerator releases it";
# the *server-side* proof that the stream really ends lives in
# `spec/client_stream_release_spec.rb`, against a real `GRPC::RpcServer` --
# an example against a live SpiceDB cannot see the server's view of its own
# stream. See root DESIGN.md, "RULE: Abandoning a stream must release it".
#
# This example used to be excluded from `mage integrationTest`, first by
# `rspec --tag ~watch` and then by directory name; it now runs.
# Bounds the wait for the update these examples write. Generous for a local
# SpiceDB -- the point is that a stalled stream fails the example with a
# message instead of hanging the job forever.
WATCH_UPDATE_TIMEOUT = 30

RSpec.describe 'WatchChanges' do
  it 'receives the relationship update it wrote, and stops at it' do
    # The shared hook clears `document` before every example, so the writes
    # below are real changes: a TOUCH of an already-identical relationship is
    # not a change, and SpiceDB emits no watch event for it.
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    revision = client.write(txn)

    # Written after the subscription revision is fixed, so the stream is
    # guaranteed to carry it and the consumer below cannot block on the happy
    # path.
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'editor', 'user', 'bob'))
    client.write(txn)

    events = client.updates(['document'], start_revision: revision)

    observed = nil
    resume_token = nil
    Timeout.timeout(WATCH_UPDATE_TIMEOUT, nil, "no watch event arrived within #{WATCH_UPDATE_TIMEOUT}s for a " \
                                               'relationship written after the subscription revision') do
      events.each do |event|
        expect(event).to be_a(SpiceDB::WatchEvent)
        next if event.updates.empty?

        # event.changes_through is a resume point: keep it and pass it as
        # start_revision on a later #updates call to pick back up after a
        # dropped stream, instead of reprocessing everything since the original
        # start_revision or silently losing changes by restarting from head.
        resume_token = event.changes_through
        observed = event.updates
        # Abandon the stream here -- see the file comment above.
        break
      end
    end

    expect(resume_token).not_to be_empty

    # Exactly the one update written after the subscription revision, and it is
    # the one that was written -- not merely "an update".
    expect(observed.length).to eq(1)
    update = observed.first
    expect(update).to be_a(SpiceDB::Update)
    expect(update.relationship.resource_type).to eq('document')
    expect(update.relationship.resource_id).to eq('seconddoc')
    expect(update.relationship.resource_relation).to eq('editor')
    expect(update.relationship.subject_type).to eq('user')
    expect(update.relationship.subject_id).to eq('bob')
    # TOUCH is a write, so it can only be the mapping for an explicit
    # OPERATION_TOUCH -- never a default an unrecognized operation falls into.
    expect(%i[create touch]).to include(update.operation)
  end

  it 'receives a checkpoint event distinguishable from an update event' do
    # include_checkpoints: true asks the server for periodic checkpoint
    # events in addition to relationship updates -- recommended if this
    # SpiceDB instance is running behind a proxy that aborts idle
    # connections, since a checkpoint keeps the stream alive even when
    # nothing has changed. A checkpoint carries no updates, so a consumer
    # must check `is_checkpoint` to tell "nothing changed, here is a fresh
    # resume point" from "here are changes".
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    revision = client.write(txn)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'thirddoc', 'viewer', 'user', 'carol'))
    client.write(txn)

    events_enum = client.updates(['document'], start_revision: revision, include_checkpoints: true)

    seen_checkpoint = false
    seen_update = false
    checkpoints = []

    Timeout.timeout(WATCH_UPDATE_TIMEOUT, nil, 'did not observe both a checkpoint and an update within ' \
                                               "#{WATCH_UPDATE_TIMEOUT}s -- include_checkpoints did not reach " \
                                               'the server, or updates are not being delivered') do
      events_enum.each do |event|
        checkpoints << event if event.is_checkpoint
        seen_checkpoint ||= event.is_checkpoint
        seen_update ||= !event.updates.empty?

        break if seen_checkpoint && seen_update
      end
    end

    expect(seen_checkpoint).to be true
    expect(seen_update).to be true
    # A checkpoint carries no updates. Without this, a server that set
    # is_checkpoint on every event would satisfy both flags above.
    expect(checkpoints).not_to be_empty
    checkpoints.each { |e| expect(e.updates).to be_empty }
  end
end
