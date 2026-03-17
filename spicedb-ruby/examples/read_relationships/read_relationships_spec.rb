# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "ReadRelationships" do
  it "reads relationships matching a filter" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "bob"))
    client.write(txn)

    # Read all viewer relationships on firstdoc
    filter = SpiceDB::Filter.new(resource_type: "document")
      .with_resource_id("firstdoc")
      .with_relation("viewer")

    relationships = client.read_relationships(SpiceDB::Consistency.full, filter).to_a

    expect(relationships).not_to be_empty
    expect(relationships.length).to eq(2)

    subject_ids = relationships.map(&:subject_id).sort
    expect(subject_ids).to eq(%w[alice bob])
  end

  it "returns an empty enumerator when no relationships match" do
    client.write_schema(TEST_SCHEMA)

    filter = SpiceDB::Filter.new(resource_type: "document")
      .with_resource_id("nonexistent")
      .with_relation("viewer")

    relationships = client.read_relationships(SpiceDB::Consistency.full, filter).to_a

    expect(relationships).to be_empty
  end

  it "supports filtering by subject type" do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.from_triple("document", "firstdoc", "viewer", "user", "alice"))
    client.write(txn)

    filter = SpiceDB::Filter.new(resource_type: "document")
      .with_resource_id("firstdoc")
      .with_relation("viewer")
      .with_subject_type("user")

    relationships = client.read_relationships(SpiceDB::Consistency.full, filter).to_a

    expect(relationships.length).to eq(1)
    expect(relationships.first.subject_id).to eq("alice")
  end
end
