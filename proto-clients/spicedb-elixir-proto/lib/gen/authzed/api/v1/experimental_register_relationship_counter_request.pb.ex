defmodule Authzed.Api.V1.ExperimentalRegisterRelationshipCounterRequest do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalRegisterRelationshipCounterRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string, deprecated: false

  field :relationship_filter, 2,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "relationshipFilter",
    deprecated: false
end
