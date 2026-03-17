# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "WriteRelationships" do
  it "writes relationships with touch and returns a revision" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "editor", "user", "bob"))

    revision = client.write(txn)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it "writes relationships with create operation" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.create(SpiceDB::Relationship.from_triple("document", "newdoc", "owner", "user", "charlie"))

    revision = client.write(txn)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it "supports preconditions with must_not_match" do
    client.write_schema(TEST_SCHEMA)

    # Ensure mallory is not already an owner before writing
    precondition_filter = SpiceDB::Filter.new(resource_type: "document")
      .with_resource_id("firstdoc")
      .with_relation("owner")
      .with_subject_type("user")
      .with_subject_id("mallory")

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    txn.must_not_match(precondition_filter)

    revision = client.write(txn)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it "supports delete operations" do
    client.write_schema(TEST_SCHEMA)

    # First, create a relationship
    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    client.write(txn)

    # Then, delete it
    txn = SpiceDB::Transaction.new
    txn.delete(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))

    revision = client.write(txn)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end
end
