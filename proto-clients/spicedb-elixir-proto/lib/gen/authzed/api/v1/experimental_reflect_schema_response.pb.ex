defmodule Authzed.Api.V1.ExperimentalReflectSchemaResponse do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalReflectSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :definitions, 1, repeated: true, type: Authzed.Api.V1.ExpDefinition
  field :caveats, 2, repeated: true, type: Authzed.Api.V1.ExpCaveat
  field :read_at, 3, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
