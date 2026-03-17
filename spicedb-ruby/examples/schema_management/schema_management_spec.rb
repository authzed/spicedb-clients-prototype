# frozen_string_literal: true

require_relative "../spec_helper"

RSpec.describe "SchemaManagement" do
  it "writes a schema and returns a revision" do
    revision = client.write_schema(TEST_SCHEMA)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
  end

  it "reads back a previously written schema" do
    client.write_schema(TEST_SCHEMA)

    schema_text, revision = client.read_schema

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty
    expect(schema_text).to include("definition user")
    expect(schema_text).to include("definition document")
    expect(schema_text).to include("permission view")
  end

  it "overwrites schema with a new version" do
    client.write_schema(TEST_SCHEMA)

    updated_schema = <<~SCHEMA
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

    revision = client.write_schema(updated_schema)

    expect(revision).not_to be_nil
    expect(revision).not_to be_empty

    schema_text, _rev = client.read_schema
    expect(schema_text).to include("relation admin")
    expect(schema_text).to include("permission manage")
  end
end
