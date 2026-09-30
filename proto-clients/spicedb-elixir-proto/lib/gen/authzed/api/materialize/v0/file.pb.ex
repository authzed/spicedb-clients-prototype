defmodule Authzed.Api.Materialize.V0.File do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.File",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :url, 2, type: :string
end
