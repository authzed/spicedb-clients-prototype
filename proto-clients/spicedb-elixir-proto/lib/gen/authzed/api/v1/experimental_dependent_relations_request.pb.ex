defmodule Authzed.Api.V1.ExperimentalDependentRelationsRequest do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalDependentRelationsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :definition_name, 2, type: :string, json_name: "definitionName"
  field :permission_name, 3, type: :string, json_name: "permissionName"
end
