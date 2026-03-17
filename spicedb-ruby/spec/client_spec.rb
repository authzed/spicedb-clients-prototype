# frozen_string_literal: true

require_relative "../lib/spicedb"

RSpec.describe SpiceDB::Client do
  describe ".new_plaintext" do
    it "creates a client" do
      client = described_class.new_plaintext("localhost:50051", "testtoken")
      expect(client).to be_a(described_class)
    end

    it "supports block form" do
      client_ref = nil
      described_class.new_plaintext("localhost:50051", "testtoken") do |client|
        client_ref = client
        expect(client).to be_a(described_class)
      end
      expect(client_ref).to be_a(described_class)
    end
  end

  describe ".new_system_tls" do
    it "creates a client" do
      client = described_class.new_system_tls("grpc.example.com:443", "my-token")
      expect(client).to be_a(described_class)
    end

    it "supports block form" do
      client_ref = nil
      described_class.new_system_tls("grpc.example.com:443", "my-token") do |client|
        client_ref = client
        expect(client).to be_a(described_class)
      end
      expect(client_ref).to be_a(described_class)
    end
  end

  describe "constants" do
    it "has sensible page size defaults" do
      expect(described_class::DEFAULT_READ_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_LOOKUP_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_EXPORT_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_DELETE_PAGE_SIZE).to eq(10_000)
      expect(described_class::DEFAULT_IMPORT_BATCH_SIZE).to eq(1_000)
      expect(described_class::DEFAULT_CHECK_BATCH_SIZE).to eq(1_000)
    end
  end

  describe "result types" do
    describe "SpiceDB::Update" do
      it "is a Data.define value type" do
        update = SpiceDB::Update.new(
          operation: :create,
          relationship: SpiceDB::Relationship.from_triple("document", "doc1", "viewer", "user", "alice")
        )
        expect(update.operation).to eq(:create)
        expect(update.relationship.subject_id).to eq("alice")
        expect(update).to be_frozen
      end
    end

    describe "SpiceDB::ExpandResult" do
      it "is a Data.define value type" do
        result = SpiceDB::ExpandResult.new(tree_root: { "type" => "union" }, revision: "rev1")
        expect(result.revision).to eq("rev1")
        expect(result).to be_frozen
      end
    end

    describe "SpiceDB::CountResult" do
      it "is a Data.define value type" do
        result = SpiceDB::CountResult.new(relationship_count: 42, revision: "rev1", still_calculating: false)
        expect(result.relationship_count).to eq(42)
        expect(result.still_calculating).to be false
        expect(result).to be_frozen
      end
    end

    describe "SpiceDB::SchemaDiff" do
      it "supports optional fields" do
        diff = SpiceDB::SchemaDiff.new(kind: "definition_added", definition_name: "user")
        expect(diff.kind).to eq("definition_added")
        expect(diff.definition_name).to eq("user")
        expect(diff.relation_name).to be_nil
        expect(diff.permission_name).to be_nil
        expect(diff.caveat_name).to be_nil
      end
    end

    describe "SpiceDB::ReflectSchemaResult" do
      it "holds definitions, caveats, and revision" do
        result = SpiceDB::ReflectSchemaResult.new(definitions: [], caveats: [], revision: "rev1")
        expect(result.definitions).to eq([])
        expect(result.caveats).to eq([])
        expect(result.revision).to eq("rev1")
      end
    end

    describe "SpiceDB::RelationReference" do
      it "holds definition_name, relation_name, and is_permission" do
        ref = SpiceDB::RelationReference.new(
          definition_name: "document",
          relation_name: "viewer",
          is_permission: true
        )
        expect(ref.definition_name).to eq("document")
        expect(ref.is_permission).to be true
      end
    end
  end

  describe "method existence" do
    let(:client) { described_class.new_plaintext("localhost:50051", "testtoken") }

    # Checks
    it { expect(client).to respond_to(:check_permission) }
    it { expect(client).to respond_to(:check_permissions) }
    it { expect(client).to respond_to(:check_any) }
    it { expect(client).to respond_to(:check_all) }

    # Relationships
    it { expect(client).to respond_to(:write) }
    it { expect(client).to respond_to(:read_relationships) }
    it { expect(client).to respond_to(:delete_relationships) }

    # Lookups
    it { expect(client).to respond_to(:lookup_resources) }
    it { expect(client).to respond_to(:lookup_subjects) }

    # Schema
    it { expect(client).to respond_to(:read_schema) }
    it { expect(client).to respond_to(:write_schema) }
    it { expect(client).to respond_to(:reflect_schema) }
    it { expect(client).to respond_to(:computable_permissions) }
    it { expect(client).to respond_to(:dependent_relations) }
    it { expect(client).to respond_to(:diff_schema) }

    # Expand
    it { expect(client).to respond_to(:expand_permission_tree) }

    # Bulk
    it { expect(client).to respond_to(:import_relationships) }
    it { expect(client).to respond_to(:export_relationships) }

    # Watch
    it { expect(client).to respond_to(:updates) }

    # Experimental
    it { expect(client).to respond_to(:experimental_register_relationship_counter) }
    it { expect(client).to respond_to(:experimental_count_relationships) }
    it { expect(client).to respond_to(:experimental_unregister_relationship_counter) }
  end
end
