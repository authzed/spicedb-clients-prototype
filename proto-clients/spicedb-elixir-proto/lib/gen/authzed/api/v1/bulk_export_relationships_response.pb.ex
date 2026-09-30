defmodule Authzed.Api.V1.BulkExportRelationshipsResponse do
  @moduledoc """
  BulkExportRelationshipsResponse is one page in a stream of relationship
  groups that meet the criteria specified by the originating request. The
  server will continue to stream back relationship groups as quickly as it can
  until all relationships have been transmitted back.
  """

  use Protobuf,
    full_name: "authzed.api.v1.BulkExportRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :after_result_cursor, 1, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
  field :relationships, 2, repeated: true, type: Authzed.Api.V1.Relationship
end
