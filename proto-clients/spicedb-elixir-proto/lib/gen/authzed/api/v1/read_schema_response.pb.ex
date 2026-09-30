defmodule Authzed.Api.V1.ReadSchemaResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReadSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :schema_text, 1, type: :string, json_name: "schemaText"
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt", deprecated: false
end
