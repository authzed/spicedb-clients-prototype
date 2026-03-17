# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "LookupSubjects" do
  it "finds all subjects with access to a resource" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "editor", "user", "bob"))
    client.write(txn)

    # Both alice (viewer) and bob (editor) can view
    subject_ids = client.lookup_subjects(
      SpiceDB::Consistency.full,
      "document", "firstdoc", "view", "user"
    ).to_a

    expect(subject_ids).to include("alice")
    expect(subject_ids).to include("bob")
  end

  it "returns only subjects with the specific permission" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "owner", "user", "bob"))
    client.write(txn)

    # Only bob (owner) can delete
    subject_ids = client.lookup_subjects(
      SpiceDB::Consistency.full,
      "document", "firstdoc", "delete", "user"
    ).to_a

    expect(subject_ids).to include("bob")
    expect(subject_ids).not_to include("alice")
  end

  it "returns empty when no subjects have access" do
    client.write_schema(TEST_SCHEMA)

    subject_ids = client.lookup_subjects(
      SpiceDB::Consistency.full,
      "document", "nonexistent", "view", "user"
    ).to_a

    expect(subject_ids).to be_empty
  end
end
