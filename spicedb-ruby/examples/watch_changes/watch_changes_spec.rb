# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'WatchChanges' do
  # `mage integrationTest` skips this whole directory by name -- see
  # skippedExamples in ../../Magefile.go -- matching the other idiomatic
  # clients' watch examples being excluded from the standard integration run
  # (long-lived streaming call vs. the request/response examples elsewhere in
  # this directory). The skip is printed with its reason and counted against
  # the expected example count, so it cannot go quiet.
  #
  # It used to be excluded by `rspec examples/ --tag ~watch` instead, and the
  # :watch tag below is what that matched. A tag filter that matches nothing
  # exits 0, which is how this spec came to be tracked, documented, and never
  # once executed in CI: the flag predates the file. The tag is kept because
  # `--tag watch` is still a convenient way to run only these, but nothing
  # depends on it for exclusion any more. Run this file explicitly with:
  #   bundle exec rspec examples/watch_changes/watch_changes_spec.rb
  it 'receives relationship updates via the watch API', :watch do
    client.write_schema(TEST_SCHEMA)

    # Write a relationship so we have a starting revision
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'firstdoc', 'viewer', 'user', 'alice'))
    revision = client.write(txn)

    # Start watching from the revision before the write so we see it
    events_enum = client.updates(['document'], start_revision: revision)

    # Write another relationship that the watcher should pick up
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple('document', 'seconddoc', 'viewer', 'user', 'bob'))
    client.write(txn)

    # Take the first event with updates from the enumerator
    event = events_enum.find { |e| !e.updates.empty? }

    expect(event).not_to be_nil
    expect(event).to be_a(SpiceDB::WatchEvent)
    # event.changes_through is a resume point: keep it and pass it as
    # start_revision on a later #updates call to pick back up after a
    # dropped stream, instead of reprocessing everything since the original
    # start_revision or silently losing changes by restarting from head.
    expect(event.changes_through).not_to be_empty

    update = event.updates.first
    expect(update).to be_a(SpiceDB::Update)
    expect(update.operation).not_to be_nil
    expect(update.relationship).not_to be_nil
    expect(update.relationship.resource_type).to eq('document')
  end

  # Tagged :watch for the same reason as above.
  it 'receives a checkpoint event distinguishable from an update event', :watch do
    client.write_schema(TEST_SCHEMA)

    # include_checkpoints: true asks the server for periodic checkpoint
    # events in addition to relationship updates -- recommended if this
    # SpiceDB instance is running behind a proxy that aborts idle
    # connections, since a checkpoint keeps the stream alive even when
    # nothing has changed. A checkpoint carries no updates, so a consumer
    # must check `is_checkpoint` to tell "nothing changed, here is a fresh
    # resume point" from "here are changes".
    events_enum = client.updates(['document'], include_checkpoints: true)

    seen_checkpoint = false
    seen_update = false
    wrote = false

    events = []
    events_enum.each do |event|
      events << event
      seen_checkpoint ||= event.is_checkpoint
      seen_update ||= !event.updates.empty?

      unless wrote
        txn = SpiceDB::Transaction.new
        txn.touch(SpiceDB::Relationship.from_triple('document', 'thirddoc', 'viewer', 'user', 'carol'))
        client.write(txn)
        wrote = true
      end

      break if seen_checkpoint && seen_update
    end

    expect(seen_checkpoint).to be true
    expect(seen_update).to be true
    events.select(&:is_checkpoint).each { |e| expect(e.updates).to be_empty }
  end
end
