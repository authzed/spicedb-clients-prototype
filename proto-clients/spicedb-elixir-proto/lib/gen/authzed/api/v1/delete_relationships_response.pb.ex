defmodule Authzed.Api.V1.DeleteRelationshipsResponse do
  use Protobuf,
    full_name: "authzed.api.v1.DeleteRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :deleted_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "deletedAt"

  field :deletion_progress, 2,
    type: Authzed.Api.V1.DeleteRelationshipsResponse.DeletionProgress,
    json_name: "deletionProgress",
    enum: true

  field :relationships_deleted_count, 3, type: :uint64, json_name: "relationshipsDeletedCount"
  field :after_result_cursor, 4, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
end
