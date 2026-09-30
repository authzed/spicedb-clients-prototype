defmodule Authzed.Api.V1.ReadCounterValue do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReadCounterValue",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relationship_count, 1, type: :uint64, json_name: "relationshipCount"
  field :read_at, 2, type: Authzed.Api.V1.ZedToken, json_name: "readAt", deprecated: false
end
