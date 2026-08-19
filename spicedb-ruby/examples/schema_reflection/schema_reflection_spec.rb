# frozen_string_literal: true

require_relative '../spec_helper'

RSpec.describe 'SchemaReflection' do
  before do
    client.write_schema(TEST_SCHEMA)
  end

  it 'reflects the current schema definitions' do
    result = client.reflect_schema(SpiceDB::Consistency.full)

    expect(result).to be_a(SpiceDB::ReflectSchemaResult)
    expect(result.definitions).not_to be_empty
    expect(result.revision).not_to be_nil

    definition_names = result.definitions.map(&:name)
    expect(definition_names).to include('document')
    expect(definition_names).to include('user')

    # The exact shape, not merely "something came back". A non-empty check
    # passes on a reflection that returned one definition with no relations and
    # no permissions -- which is what dropping the nested conversion loops
    # would produce. Relations and permissions are also distinct lists: a
    # reflection that conflated them would put `view` among the relations.
    doc_def = result.definitions.find { |d| d.name == 'document' }
    expect(doc_def.relations.map(&:name).sort).to eq(%w[editor owner viewer])
    expect(doc_def.permissions.map(&:name).sort).to eq(%w[delete edit view])
  end

  it 'finds computable permissions for a relation' do
    permissions, revision = client.computable_permissions(
      SpiceDB::Consistency.full,
      'document',
      'viewer'
    )

    expect(revision).not_to be_nil
    expect(permissions).not_to be_empty

    # `viewer` appears in `view` and in nothing else in TEST_SCHEMA, so the
    # answer is exactly one reference -- and it names the permission rather
    # than the relation it was asked about.
    expect(permissions.map { |p| "#{p.definition_name}##{p.relation_name}" }).to eq(['document#view'])
    expect(permissions.first.is_permission).to be true
  end

  it 'finds dependent relations for a permission' do
    relations, revision = client.dependent_relations(
      SpiceDB::Consistency.full,
      'document',
      'view'
    )

    expect(revision).not_to be_nil
    expect(relations).not_to be_empty

    # `view = viewer + editor + owner`, so all three relations are
    # dependencies and nothing else is.
    expect(relations.map { |r| "#{r.definition_name}##{r.relation_name}" }.sort)
      .to eq(['document#editor', 'document#owner', 'document#viewer'])
  end

  it 'diffs the current schema against a modified schema' do
    new_schema = <<~SCHEMA
      definition user {}

      definition document {
      	relation viewer: user
      	relation editor: user
      	relation owner: user
      	relation admin: user
      	permission view = viewer + editor + owner + admin
      	permission edit = editor + owner + admin
      	permission delete = owner + admin
      	permission manage = admin
      }
    SCHEMA

    diffs, revision = client.diff_schema(SpiceDB::Consistency.full, new_schema)

    expect(revision).not_to be_nil
    expect(diffs).not_to be_empty

    # `expect(diffs.map(&:kind)).not_to be_empty` used to stand here, two lines
    # after asserting `diffs` itself is non-empty: `map` preserves length, so
    # it cannot fail. new_schema adds one relation and one permission and
    # changes the expression of the other three, so the diff is a known set --
    # and a mapping that reported every diff as :unknown now fails.
    diff_tuples = diffs.map { |d| [d.kind, d.definition_name, d.relation_name, d.permission_name] }
    expect(diff_tuples).to include(['relation_added', 'document', 'admin', nil])
    expect(diff_tuples).to include(['permission_added', 'document', nil, 'manage'])
    expect(diff_tuples).to include(['permission_expr_changed', 'document', nil, 'view'])
    expect(diffs.map(&:kind)).not_to include('unknown')
  end
end
