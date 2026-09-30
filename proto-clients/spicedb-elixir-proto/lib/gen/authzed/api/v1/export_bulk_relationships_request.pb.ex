defmodule Authzed.Api.V1.ExportBulkRelationshipsRequest do
  @moduledoc """
  ExportBulkRelationshipsRequest represents a resumable request for
  all relationships from the server.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ExportBulkRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :optional_limit, 2, type: :uint32, json_name: "optionalLimit", deprecated: false
  field :optional_cursor, 3, type: Authzed.Api.V1.Cursor, json_name: "optionalCursor"

  field :optional_relationship_filter, 4,
    type: Authzed.Api.V1.RelationshipFilter,
    json_name: "optionalRelationshipFilter"
end
