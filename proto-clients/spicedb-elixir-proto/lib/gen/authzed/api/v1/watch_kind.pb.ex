defmodule Authzed.Api.V1.WatchKind do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.WatchKind",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :WATCH_KIND_UNSPECIFIED, 0
  field :WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES, 1
  field :WATCH_KIND_INCLUDE_SCHEMA_UPDATES, 2
  field :WATCH_KIND_INCLUDE_CHECKPOINTS, 3
end
