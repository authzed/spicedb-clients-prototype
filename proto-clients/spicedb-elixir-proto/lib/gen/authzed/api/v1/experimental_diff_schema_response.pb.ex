defmodule Authzed.Api.V1.ExperimentalDiffSchemaResponse do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalDiffSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :diffs, 1, repeated: true, type: Authzed.Api.V1.ExpSchemaDiff
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
