defmodule Authzed.Api.Materialize.V0.ExperimentalRoaringLookupResourcesResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.ExperimentalRoaringLookupResourcesResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :bitmap, 1, type: :bytes
  field :cardinality, 2, type: :uint64
  field :at_revision, 3, type: Authzed.Api.V1.ZedToken, json_name: "atRevision", deprecated: false
end
