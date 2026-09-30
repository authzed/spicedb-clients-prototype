defmodule Authzed.Api.V1.ExpRelation do
  @moduledoc """
  ExpRelation is the representation of a relation in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ExpRelation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :comment, 2, type: :string
  field :parent_definition_name, 3, type: :string, json_name: "parentDefinitionName"

  field :subject_types, 4,
    repeated: true,
    type: Authzed.Api.V1.ExpTypeReference,
    json_name: "subjectTypes"
end
