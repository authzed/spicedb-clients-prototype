defmodule Authzed.Api.Materialize.V0.WatchPermissionsRequest do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.WatchPermissionsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :permissions, 1, repeated: true, type: Authzed.Api.Materialize.V0.WatchedPermission

  field :optional_starting_after, 2,
    type: Authzed.Api.V1.ZedToken,
    json_name: "optionalStartingAfter"
end
