defmodule Authzed.Api.V1.Precondition do
  @moduledoc """
  Precondition specifies how and the existence or absence of certain
  relationships as expressed through the accompanying filter should affect
  whether or not the operation proceeds.

  MUST_NOT_MATCH will fail the parent request if any relationships match the
  relationships filter.
  MUST_MATCH will fail the parent request if there are no
  relationships that match the filter.
  """

  use Protobuf,
    full_name: "authzed.api.v1.Precondition",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :operation, 1, type: Authzed.Api.V1.Precondition.Operation, enum: true, deprecated: false
  field :filter, 2, type: Authzed.Api.V1.RelationshipFilter, deprecated: false
end
