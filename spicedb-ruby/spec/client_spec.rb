# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe SpiceDB::Client do
  describe '.new_plaintext' do
    it 'creates a client' do
      client = described_class.new_plaintext('localhost:50051', 'testtoken')
      expect(client).to be_a(described_class)
    end

    it 'supports block form' do
      client_ref = nil
      described_class.new_plaintext('localhost:50051', 'testtoken') do |client|
        client_ref = client
        expect(client).to be_a(described_class)
      end
      expect(client_ref).to be_a(described_class)
    end
  end

  describe '.new_system_tls' do
    it 'creates a client' do
      client = described_class.new_system_tls('grpc.example.com:443', 'my-token')
      expect(client).to be_a(described_class)
    end

    it 'supports block form' do
      client_ref = nil
      described_class.new_system_tls('grpc.example.com:443', 'my-token') do |client|
        client_ref = client
        expect(client).to be_a(described_class)
      end
      expect(client_ref).to be_a(described_class)
    end
  end

  # Regression coverage for root DESIGN.md, "RULE: Credentials over insecure
  # transport require an explicit opt-in". The proto-layer spec
  # (spec/client_spec.rb in spicedb-ruby-proto,
  # "SpiceDBProto::Client insecure host guard") is what proves the credential
  # itself never reaches the wire -- via GRPC::Core::Channel/
  # BearerTokenInterceptor message expectations; these tests prove the
  # idiomatic constructor actually reaches, and propagates, that same guard.
  describe 'insecure host guard' do
    it 'refuses a non-loopback endpoint without the opt-in' do
      expect do
        described_class.new_plaintext('evil.example.com:1234', 'testtoken')
      end.to raise_error(SpiceDB::InvalidArgumentError, /allow_insecure_remote_credentials/)
    end

    # The bypass shapes, at the public entry point. The proto layer holds the full
    # fixture set and the "credential never reaches the wire" proof; these are the
    # ones that must not slip through .new_plaintext itself.
    [
      '127.0.0.1:443@evil.com',
      '[::1]:443@evil.com',
      '[::1]:0@127.0.0.1:19999',
      'localhost@evil.com',
      '127.0.0.1:notaport'
    ].each do |endpoint|
      it "refuses authority-shifting #{endpoint.inspect} without the opt-in" do
        expect do
          described_class.new_plaintext(endpoint, 'testtoken')
        end.to raise_error(SpiceDB::InvalidArgumentError, /allow_insecure_remote_credentials/)
      end
    end

    it 'allows a loopback endpoint with no opt-in' do
      expect do
        described_class.new_plaintext('localhost:50051', 'testtoken')
      end.not_to raise_error
    end

    # Unlike C#, TypeScript and Rust -- whose transports cannot dial a UDS path and
    # so now refuse these -- grpc-ruby's C-core genuinely honours a unix: target,
    # which is why the exemption stays here.
    ['unix:/var/run/spicedb.sock', 'unix:///var/run/spicedb.sock'].each do |endpoint|
      it "allows unix-socket target #{endpoint.inspect} with no opt-in" do
        expect do
          described_class.new_plaintext(endpoint, 'testtoken')
        end.not_to raise_error
      end
    end

    it 'allows a non-loopback endpoint when allow_insecure_remote_credentials is true' do
      expect do
        described_class.new_plaintext(
          'evil.example.com:1234', 'testtoken', allow_insecure_remote_credentials: true
        )
      end.not_to raise_error
    end
  end

  describe 'constants' do
    it 'has sensible page size defaults' do
      expect(described_class::DEFAULT_READ_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_LOOKUP_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_EXPORT_PAGE_SIZE).to eq(512)
      expect(described_class::DEFAULT_DELETE_PAGE_SIZE).to eq(1_000)
      expect(described_class::DEFAULT_IMPORT_BATCH_SIZE).to eq(1_000)
      expect(described_class::DEFAULT_CHECK_BATCH_SIZE).to eq(1_000)
    end
  end

  describe 'result types' do
    describe 'SpiceDB::Update' do
      it 'is a Data.define value type' do
        update = SpiceDB::Update.new(
          operation: :create,
          relationship: SpiceDB::Relationship.from_triple('document', 'doc1', 'viewer', 'user', 'alice')
        )
        expect(update.operation).to eq(:create)
        expect(update.relationship.subject_id).to eq('alice')
        expect(update).to be_frozen
      end
    end

    describe 'SpiceDB::ExpandResult' do
      it 'is a Data.define value type holding a native PermissionTree, not a proto' do
        tree = SpiceDB::PermissionTree.new(
          expanded_object: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'),
          expanded_relation: 'view',
          intermediate: nil,
          leaf: SpiceDB::LeafNode.new(subjects: [])
        )
        result = SpiceDB::ExpandResult.new(tree: tree, revision: 'rev1')
        expect(result.tree).to eq(tree)
        expect(result.revision).to eq('rev1')
        expect(result).to be_frozen
        expect(result.tree).not_to be_a(Authzed::Api::V1::PermissionRelationshipTree)
      end
    end

    describe 'SpiceDB::PermissionTree' do
      it 'holds exactly one of intermediate or leaf' do
        leaf_tree = SpiceDB::PermissionTree.new(
          expanded_object: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'),
          expanded_relation: 'view',
          intermediate: nil,
          leaf: SpiceDB::LeafNode.new(
            subjects: [SpiceDB::SubjectRef.new(subject_type: 'user', subject_id: 'alice', optional_relation: '')]
          )
        )
        expect(leaf_tree.leaf.subjects.first.subject_id).to eq('alice')
        expect(leaf_tree.intermediate).to be_nil
        expect(leaf_tree).to be_frozen
      end
    end

    describe 'SpiceDB::CountResult' do
      it 'is a Data.define value type' do
        result = SpiceDB::CountResult.new(relationship_count: 42, revision: 'rev1', still_calculating: false)
        expect(result.relationship_count).to eq(42)
        expect(result.still_calculating).to be false
        expect(result).to be_frozen
      end
    end

    describe 'SpiceDB::SchemaDiff' do
      it 'supports optional fields' do
        diff = SpiceDB::SchemaDiff.new(kind: 'definition_added', definition_name: 'user')
        expect(diff.kind).to eq('definition_added')
        expect(diff.definition_name).to eq('user')
        expect(diff.relation_name).to be_nil
        expect(diff.permission_name).to be_nil
        expect(diff.caveat_name).to be_nil
      end
    end

    describe 'SpiceDB::ReflectSchemaResult' do
      it 'holds definitions, caveats, and revision' do
        result = SpiceDB::ReflectSchemaResult.new(definitions: [], caveats: [], revision: 'rev1')
        expect(result.definitions).to eq([])
        expect(result.caveats).to eq([])
        expect(result.revision).to eq('rev1')
      end
    end

    describe 'SpiceDB::RelationReference' do
      it 'holds definition_name, relation_name, and is_permission' do
        ref = SpiceDB::RelationReference.new(
          definition_name: 'document',
          relation_name: 'viewer',
          is_permission: true
        )
        expect(ref.definition_name).to eq('document')
        expect(ref.is_permission).to be true
      end
    end
  end

  describe 'method existence' do
    let(:client) { described_class.new_plaintext('localhost:50051', 'testtoken') }

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
