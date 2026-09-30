defmodule Authzed.Api.V1.Precondition do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.Precondition",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :operation, 1, type: Authzed.Api.V1.Precondition.Operation, enum: true, deprecated: false
  field :filter, 2, type: Authzed.Api.V1.RelationshipFilter, deprecated: false
end
