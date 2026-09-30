defmodule Authzed.Api.V1.DiffSchemaResponse do
  use Protobuf,
    full_name: "authzed.api.v1.DiffSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :diffs, 1, repeated: true, type: Authzed.Api.V1.ReflectionSchemaDiff
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
