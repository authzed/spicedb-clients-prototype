defmodule Authzed.Api.V1.ComputablePermissionsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ComputablePermissionsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :permissions, 1, repeated: true, type: Authzed.Api.V1.ReflectionRelationReference
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
