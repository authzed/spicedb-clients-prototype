# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'ExpandPermissionTree' do
  # Recursively walks a SpiceDB::PermissionTree, collecting every subject_id
  # found in leaf nodes — this is the shape a caller walks to render or
  # debug the access structure behind a permission decision.
  def collect_leaf_subject_ids(tree)
    return [] if tree.nil?

    if tree.leaf
      tree.leaf.subjects.map { |subject| subject.object.object_id }
    elsif tree.intermediate
      tree.intermediate.children.flat_map { |child| collect_leaf_subject_ids(child) }
    else
      []
    end
  end

  it 'expands a union permission into intermediate and leaf nodes' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'viewer',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'alice'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'editor',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user', object_id: 'bob'), relation: nil)))
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'owner',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'carol'), relation: nil)))
    client.write(txn)

    result = client.expand_permission_tree(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'),
                                           permission: 'view', consistency: SpiceDB::Consistency.full)

    expect(result).to be_a(SpiceDB::ExpandResult)
    expect(result.revision).not_to be_nil

    tree = result.tree
    expect(tree).to be_a(SpiceDB::PermissionTree)
    expect(tree.expanded_object).to eq(SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'))
    expect(tree.expanded_relation).to eq('view')

    # "view" is defined as `viewer + editor + owner` — a union — so the root
    # node is an IntermediateNode, not a leaf.
    expect(tree.intermediate).not_to be_nil
    expect(tree.intermediate).to be_a(SpiceDB::IntermediateNode)
    expect(tree.intermediate.operation).to eq(:union)

    # Walk the whole tree and confirm every direct grant surfaces somewhere
    # among the leaves.
    subject_ids = collect_leaf_subject_ids(tree)
    expect(subject_ids).to include('alice', 'bob', 'carol')
  end

  it 'expands a permission backed by a single relation down to a leaf' do
    client.write_schema(TEST_SCHEMA)

    txn = SpiceDB::Transaction.new
    txn.touch(SpiceDB::Relationship.new(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), relation: 'owner',
                                        subject: SpiceDB::SubjectRef.new(object: SpiceDB::ObjectRef.new(object_type: 'user',
                                                                                                        object_id: 'carol'), relation: nil)))
    client.write(txn)

    # "delete" is defined as just `owner` — walk the tree (rather than
    # assuming a fixed shape) since a single-relation permission may surface
    # as a bare LeafNode or as an intermediate wrapping one.
    result = client.expand_permission_tree(resource: SpiceDB::ObjectRef.new(object_type: 'document', object_id: 'firstdoc'), permission: 'delete',
                                           consistency: SpiceDB::Consistency.full)

    expect(collect_leaf_subject_ids(result.tree)).to include('carol')
  end
end
