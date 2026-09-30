defmodule Authzed.Api.V1.BulkImportRelationshipsRequest do
  @moduledoc """
  BulkImportRelationshipsRequest represents one batch of the streaming
  BulkImportRelationships API. The maximum size is only limited by the backing
  datastore, and optimal size should be determined by the calling client
  experimentally. When BulkImport is invoked and receives its first request message,
  a transaction is opened to import the relationships. All requests sent to the same
  invocation are executed under this single transaction. If a relationship already
  exists within the datastore, the entire transaction will fail with an error.
  """

  use Protobuf,
    full_name: "authzed.api.v1.BulkImportRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationships, 1, repeated: true, type: Authzed.Api.V1.Relationship, deprecated: false
end
