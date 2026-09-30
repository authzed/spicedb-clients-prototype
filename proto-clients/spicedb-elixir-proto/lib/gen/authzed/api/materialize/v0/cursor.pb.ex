defmodule Authzed.Api.Materialize.V0.Cursor do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.Cursor",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :limit, 1, type: :uint32
  field :token, 4, type: Authzed.Api.V1.ZedToken
  field :starting_index, 5, type: :uint32, json_name: "startingIndex"
  field :completed_members, 6, type: :bool, json_name: "completedMembers"
  field :starting_key, 7, type: :string, json_name: "startingKey"
  field :cursor, 8, type: :string
end
