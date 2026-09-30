defmodule Authzed.Api.V1.BulkImportRelationshipsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.BulkImportRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :num_loaded, 1, type: :uint64, json_name: "numLoaded"
end
