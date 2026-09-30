defmodule Authzed.Api.V1.BulkImportRelationshipsResponse do
  @moduledoc """
  BulkImportRelationshipsResponse is returned on successful completion of the
  bulk load stream, and contains the total number of relationships loaded.
  """

  use Protobuf,
    full_name: "authzed.api.v1.BulkImportRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :num_loaded, 1, type: :uint64, json_name: "numLoaded"
end
