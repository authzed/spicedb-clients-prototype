defmodule Authzed.Api.V1.ExportBulkRelationshipsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExportBulkRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :after_result_cursor, 1, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
  field :relationships, 2, repeated: true, type: Authzed.Api.V1.Relationship
end
