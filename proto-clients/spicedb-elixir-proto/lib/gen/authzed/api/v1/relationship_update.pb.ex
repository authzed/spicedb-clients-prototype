defmodule Authzed.Api.V1.RelationshipUpdate do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.RelationshipUpdate",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :operation, 1,
    type: Authzed.Api.V1.RelationshipUpdate.Operation,
    enum: true,
    deprecated: false

  field :relationship, 2, type: Authzed.Api.V1.Relationship, deprecated: false
end
