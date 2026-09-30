defmodule Authzed.Api.V1.ExperimentalDependentRelationsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalDependentRelationsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relations, 1, repeated: true, type: Authzed.Api.V1.ExpRelationReference
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt"
end
