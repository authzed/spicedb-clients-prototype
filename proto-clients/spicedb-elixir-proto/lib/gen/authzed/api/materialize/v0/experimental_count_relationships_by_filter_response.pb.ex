defmodule Authzed.Api.Materialize.V0.ExperimentalCountRelationshipsByFilterResponse do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationship_count, 1, type: :uint64, json_name: "relationshipCount"
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt", deprecated: false
end
