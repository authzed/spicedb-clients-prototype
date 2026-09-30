defmodule Authzed.Api.Materialize.V0.WatchPermissionSetsRequest do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.WatchPermissionSetsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :optional_starting_after, 1,
    type: Authzed.Api.V1.ZedToken,
    json_name: "optionalStartingAfter"
end
