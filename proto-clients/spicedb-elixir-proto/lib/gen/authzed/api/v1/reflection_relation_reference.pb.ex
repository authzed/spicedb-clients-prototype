defmodule Authzed.Api.V1.ReflectionRelationReference do
  @moduledoc """
  ReflectionRelationReference is a reference to a relation or permission in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionRelationReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :definition_name, 1, type: :string, json_name: "definitionName"
  field :relation_name, 2, type: :string, json_name: "relationName"
  field :is_permission, 3, type: :bool, json_name: "isPermission"
end
