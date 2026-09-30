defmodule Authzed.Api.V1.ImportBulkRelationshipsRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ImportBulkRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationships, 1, repeated: true, type: Authzed.Api.V1.Relationship, deprecated: false
end
