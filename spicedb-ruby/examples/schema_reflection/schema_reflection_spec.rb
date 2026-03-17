# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "SchemaReflection" do
  before do
    client.write_schema(TEST_SCHEMA)
  end

  it "reflects the current schema definitions" do
    result = client.reflect_schema(SpiceDB::Consistency.full)

    expect(result).to be_a(SpiceDB::ReflectSchemaResult)
    expect(result.definitions).not_to be_empty
    expect(result.revision).not_to be_nil

    definition_names = result.definitions.map(&:name)
    expect(definition_names).to include("document")
    expect(definition_names).to include("user")

    doc_def = result.definitions.find { |d| d.name == "document" }
    expect(doc_def.relations).not_to be_empty
    expect(doc_def.permissions).not_to be_empty
  end

  it "finds computable permissions for a relation" do
    permissions, revision = client.computable_permissions(
      SpiceDB::Consistency.full,
      "document",
      "viewer"
    )

    expect(revision).not_to be_nil
    expect(permissions).not_to be_empty

    # The "viewer" relation should contribute to the "view" permission
    permission_names = permissions.map(&:relation_name)
    expect(permission_names).to include("view")
  end

  it "finds dependent relations for a permission" do
    relations, revision = client.dependent_relations(
      SpiceDB::Consistency.full,
      "document",
      "view"
    )

    expect(revision).not_to be_nil
    expect(relations).not_to be_empty

    # The "view" permission depends on viewer, editor, and owner
    relation_names = relations.map(&:relation_name)
    expect(relation_names).to include("viewer")
    expect(relation_names).to include("editor")
    expect(relation_names).to include("owner")
  end

  it "diffs the current schema against a modified schema" do
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

    diff_kinds = diffs.map(&:kind)
    expect(diff_kinds).not_to be_empty
  end
end
