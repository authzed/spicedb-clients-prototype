defmodule Authzed.Api.V1.ExpDefinition do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpDefinition",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :comment, 2, type: :string
  field :relations, 3, repeated: true, type: Authzed.Api.V1.ExpRelation
  field :permissions, 4, repeated: true, type: Authzed.Api.V1.ExpPermission
end
