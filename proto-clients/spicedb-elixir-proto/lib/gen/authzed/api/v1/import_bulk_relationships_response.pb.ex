defmodule Authzed.Api.V1.ImportBulkRelationshipsResponse do
  @moduledoc """
  ImportBulkRelationshipsResponse is returned on successful completion of the
  bulk load stream, and contains the total number of relationships loaded.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ImportBulkRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :num_loaded, 1, type: :uint64, json_name: "numLoaded"
end
