defmodule Authzed.Api.V1.ReflectionDefinition do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionDefinition",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :comment, 2, type: :string
  field :relations, 3, repeated: true, type: Authzed.Api.V1.ReflectionRelation
  field :permissions, 4, repeated: true, type: Authzed.Api.V1.ReflectionPermission
end
