# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "WatchChanges" do
  it "receives relationship updates via the watch API" do
    client.write_schema(TEST_SCHEMA)

    # Write a relationship so we have a starting revision
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    revision = client.write(txn)

    # Start watching from the revision before the write so we see it
    updates_enum = client.updates(["document"], start_revision: revision)

    # Write another relationship that the watcher should pick up
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "seconddoc", "viewer", "user", "bob"))
    client.write(txn)

    # Take the first update from the enumerator
    update = updates_enum.first

    expect(update).not_to be_nil
    expect(update).to be_a(SpiceDB::Update)
    expect(update.operation).not_to be_nil
    expect(update.relationship).not_to be_nil
    expect(update.relationship.resource_type).to eq("document")
  end
end
