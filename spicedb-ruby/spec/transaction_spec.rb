# frozen_string_literal: true

require_relative "../lib/spicedb"

RSpec.describe SpiceDB::Transaction do
  let(:rel) do
    SpiceDB::Relationship.from_triple(
      "document", "doc1", "viewer",
      "user", "alice"
    )
  end

  let(:rel2) do
    SpiceDB::Relationship.from_triple(
      "document", "doc2", "editor",
      "user", "bob"
    )
  end

  describe "#create" do
    it "adds a create operation" do
      txn = described_class.new
      txn.create(rel)

      expect(txn.updates.length).to eq(1)
      expect(txn.updates[0][:operation]).to eq(:create)
      expect(txn.updates[0][:relationship]).to eq(rel)
    end

    it "returns self for chaining" do
      txn = described_class.new
      result = txn.create(rel)
      expect(result).to be(txn)
    end
  end

  describe "#touch" do
    it "adds a touch operation" do
      txn = described_class.new
      txn.touch(rel)

      expect(txn.updates.length).to eq(1)
      expect(txn.updates[0][:operation]).to eq(:touch)
      expect(txn.updates[0][:relationship]).to eq(rel)
    end
  end

  describe "#delete" do
    it "adds a delete operation" do
      txn = described_class.new
      txn.delete(rel)

      expect(txn.updates.length).to eq(1)
      expect(txn.updates[0][:operation]).to eq(:delete)
      expect(txn.updates[0][:relationship]).to eq(rel)
    end
  end

  describe "multiple operations" do
    it "collects operations in order" do
      txn = described_class.new
      txn.create(rel)
      txn.touch(rel2)
      txn.delete(rel)

      expect(txn.updates.length).to eq(3)
      expect(txn.updates.map { |u| u[:operation] }).to eq(%i[create touch delete])
    end
  end

  describe "#must_not_match" do
    it "adds a must_not_match precondition" do
      txn = described_class.new
      filter = SpiceDB::Filter.new(resource_type: "document")
      txn.must_not_match(filter)

      expect(txn.preconditions.length).to eq(1)
      expect(txn.preconditions[0][:operation]).to eq(:must_not_match)
      expect(txn.preconditions[0][:filter]).to eq(filter)
    end

    it "returns self for chaining" do
      txn = described_class.new
      filter = SpiceDB::Filter.new(resource_type: "document")
      result = txn.must_not_match(filter)
      expect(result).to be(txn)
    end
  end

  describe "#must_match" do
    it "adds a must_match precondition" do
      txn = described_class.new
      filter = SpiceDB::Filter.new(resource_type: "document")
      txn.must_match(filter)

      expect(txn.preconditions.length).to eq(1)
      expect(txn.preconditions[0][:operation]).to eq(:must_match)
      expect(txn.preconditions[0][:filter]).to eq(filter)
    end
  end

  describe "#empty?" do
    it "returns true for a new transaction" do
      expect(described_class.new).to be_empty
    end

    it "returns false after adding an operation" do
      txn = described_class.new
      txn.create(rel)
      expect(txn).not_to be_empty
    end
  end
end
