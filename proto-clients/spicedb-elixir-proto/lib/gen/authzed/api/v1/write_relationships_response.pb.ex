defmodule Authzed.Api.V1.WriteRelationshipsResponse do
  use Protobuf,
    full_name: "authzed.api.v1.WriteRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :written_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "writtenAt"
end
