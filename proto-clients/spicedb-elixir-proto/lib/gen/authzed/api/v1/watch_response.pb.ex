defmodule Authzed.Api.V1.WatchResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.WatchResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :updates, 1, repeated: true, type: Authzed.Api.V1.RelationshipUpdate
  field :changes_through, 2, type: Authzed.Api.V1.ZedToken, json_name: "changesThrough"

  field :optional_transaction_metadata, 3,
    type: Google.Protobuf.Struct,
    json_name: "optionalTransactionMetadata"

  field :schema_updated, 4, type: :bool, json_name: "schemaUpdated"
  field :is_checkpoint, 5, type: :bool, json_name: "isCheckpoint"

  field :full_revision_metadata, 6,
    repeated: true,
    type: Google.Protobuf.Struct,
    json_name: "fullRevisionMetadata"
end
