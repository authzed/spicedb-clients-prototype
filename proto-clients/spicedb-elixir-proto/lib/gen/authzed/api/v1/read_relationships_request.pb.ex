defmodule Authzed.Api.V1.ReadRelationshipsRequest do
  @moduledoc """
  ReadRelationshipsRequest specifies one or more filters used to read matching
  relationships within the system.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReadRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :relationship_filter, 2,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "relationshipFilter",
    deprecated: false

  field :optional_limit, 3, type: :uint32, json_name: "optionalLimit", deprecated: false
  field :optional_cursor, 4, type: Authzed.Api.V1.Cursor, json_name: "optionalCursor"
end
