defmodule Authzed.Api.V1.AlgebraicSubjectSet do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.AlgebraicSubjectSet",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :operation, 1,
    type: Authzed.Api.V1.AlgebraicSubjectSet.Operation,
    enum: true,
    deprecated: false

  field :children, 2,
    repeated: true,
    type: Authzed.Api.V1.PermissionRelationshipTree,
    deprecated: false
end
