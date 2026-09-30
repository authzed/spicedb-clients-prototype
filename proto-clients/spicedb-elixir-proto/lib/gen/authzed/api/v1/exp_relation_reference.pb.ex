defmodule Authzed.Api.V1.ExpRelationReference do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpRelationReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :definition_name, 1, type: :string, json_name: "definitionName"
  field :relation_name, 2, type: :string, json_name: "relationName"
  field :is_permission, 3, type: :bool, json_name: "isPermission"
end
