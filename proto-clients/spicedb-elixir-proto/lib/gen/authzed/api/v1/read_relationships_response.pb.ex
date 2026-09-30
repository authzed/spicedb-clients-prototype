defmodule Authzed.Api.V1.ReadRelationshipsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReadRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :read_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "readAt", deprecated: false
  field :relationship, 2, type: Authzed.Api.V1.Relationship, deprecated: false
  field :after_result_cursor, 3, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
end
