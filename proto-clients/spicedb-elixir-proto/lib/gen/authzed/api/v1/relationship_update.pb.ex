defmodule Authzed.Api.V1.RelationshipUpdate do
  @moduledoc """
  RelationshipUpdate is used for mutating a single relationship within the
  service.

  CREATE will create the relationship only if it doesn't exist, and error
  otherwise.

  TOUCH will upsert the relationship, and will not error if it
  already exists.

  DELETE will delete the relationship. If the relationship does not exist,
  this operation will no-op.
  """

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
