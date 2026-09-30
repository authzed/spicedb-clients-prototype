defmodule Authzed.Api.V1.ExperimentalCountRelationshipsRequest do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalCountRelationshipsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string, deprecated: false
end
