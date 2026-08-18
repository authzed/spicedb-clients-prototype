# frozen_string_literal: true

require_relative '../lib/spicedb'
require 'spicedb_proto'

RSpec.describe 'SpiceDB::Client#permission_tree_from_proto' do
  let(:client) { SpiceDB::Client.new_plaintext('localhost:50051', 'testtoken') }

  # Builds a synthetic proto tree:
  #
  #   UNION (root, document:doc1#view)
  #     +-- leaf: [user:alice, group:eng#member]
  #     +-- INTERSECTION
  #           +-- leaf: [user:bob]
  def build_proto_tree
    top_leaf = Authzed::Api::V1::PermissionRelationshipTree.new(
      expanded_object: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      expanded_relation: 'view',
      leaf: Authzed::Api::V1::DirectSubjectSet.new(
        subjects: [
          Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'alice'),
            optional_relation: ''
          ),
          Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(object_type: 'group', object_id: 'eng'),
            optional_relation: 'member'
          )
        ]
      )
    )

    inner_leaf = Authzed::Api::V1::PermissionRelationshipTree.new(
      expanded_object: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      expanded_relation: 'view',
      leaf: Authzed::Api::V1::DirectSubjectSet.new(
        subjects: [
          Authzed::Api::V1::SubjectReference.new(
            object: Authzed::Api::V1::ObjectReference.new(object_type: 'user', object_id: 'bob'),
            optional_relation: ''
          )
        ]
      )
    )

    nested_intersection = Authzed::Api::V1::PermissionRelationshipTree.new(
      expanded_object: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      expanded_relation: 'view',
      intermediate: Authzed::Api::V1::AlgebraicSubjectSet.new(
        operation: :OPERATION_INTERSECTION,
        children: [inner_leaf]
      )
    )

    Authzed::Api::V1::PermissionRelationshipTree.new(
      expanded_object: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      expanded_relation: 'view',
      intermediate: Authzed::Api::V1::AlgebraicSubjectSet.new(
        operation: :OPERATION_UNION,
        children: [top_leaf, nested_intersection]
      )
    )
  end

  it 'maps a proto tree to a native SpiceDB::PermissionTree structure, field by field' do
    tree = client.send(:permission_tree_from_proto, build_proto_tree)

    expect(tree).to be_a(SpiceDB::PermissionTree)
    expect(tree.expanded_object).to eq(SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'doc1'))
    expect(tree.expanded_relation).to eq('view')
    expect(tree.leaf).to be_nil
    expect(tree.intermediate).to be_a(SpiceDB::IntermediateNode)
    expect(tree.intermediate.operation).to eq(:union)
    expect(tree.intermediate.children.length).to eq(2)

    leaf_child = tree.intermediate.children[0]
    expect(leaf_child).to be_a(SpiceDB::PermissionTree)
    expect(leaf_child.intermediate).to be_nil
    expect(leaf_child.leaf).to be_a(SpiceDB::LeafNode)
    expect(leaf_child.leaf.subjects).to eq(
      [
        SpiceDB::SubjectRef.new(subject_type: 'user', subject_id: 'alice', optional_relation: ''),
        SpiceDB::SubjectRef.new(subject_type: 'group', subject_id: 'eng', optional_relation: 'member')
      ]
    )

    nested_child = tree.intermediate.children[1]
    expect(nested_child.leaf).to be_nil
    expect(nested_child.intermediate).to be_a(SpiceDB::IntermediateNode)
    expect(nested_child.intermediate.operation).to eq(:intersection)
    expect(nested_child.intermediate.children.length).to eq(1)
    expect(nested_child.intermediate.children[0].leaf.subjects).to eq(
      [SpiceDB::SubjectRef.new(subject_type: 'user', subject_id: 'bob', optional_relation: '')]
    )
  end

  it 'maps an unspecified algebraic operation to :unspecified' do
    proto_tree = Authzed::Api::V1::PermissionRelationshipTree.new(
      expanded_object: Authzed::Api::V1::ObjectReference.new(object_type: 'document', object_id: 'doc1'),
      expanded_relation: 'view',
      intermediate: Authzed::Api::V1::AlgebraicSubjectSet.new(
        operation: :OPERATION_UNSPECIFIED,
        children: []
      )
    )

    tree = client.send(:permission_tree_from_proto, proto_tree)
    expect(tree.intermediate.operation).to eq(:unspecified)
  end

  it 'returns nil for a nil input' do
    expect(client.send(:permission_tree_from_proto, nil)).to be_nil
  end
end
