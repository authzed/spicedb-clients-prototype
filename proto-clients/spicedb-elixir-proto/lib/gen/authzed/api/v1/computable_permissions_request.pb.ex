defmodule Authzed.Api.V1.ComputablePermissionsRequest do
  use Protobuf,
    full_name: "authzed.api.v1.ComputablePermissionsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :definition_name, 2, type: :string, json_name: "definitionName"
  field :relation_name, 3, type: :string, json_name: "relationName"

  field :optional_definition_name_filter, 4,
    type: :string,
    json_name: "optionalDefinitionNameFilter"
end
