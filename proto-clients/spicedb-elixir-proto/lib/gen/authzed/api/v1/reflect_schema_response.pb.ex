defmodule Authzed.Api.V1.ReflectSchemaResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReflectSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :definitions, 1, repeated: true, type: Authzed.Api.V1.ReflectionDefinition
  field :caveats, 2, repeated: true, type: Authzed.Api.V1.ReflectionCaveat
  field :read_at, 3, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
