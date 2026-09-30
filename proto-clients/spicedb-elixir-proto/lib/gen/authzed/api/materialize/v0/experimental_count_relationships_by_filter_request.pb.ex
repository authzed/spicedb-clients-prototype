defmodule Authzed.Api.Materialize.V0.ExperimentalCountRelationshipsByFilterRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationship_filter, 1,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "relationshipFilter",
    deprecated: false
end
