defmodule Authzed.Api.Materialize.V0.WatchPermissionsResponse do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.WatchPermissionsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :response, 0

  field :change, 1, type: Authzed.Api.Materialize.V0.PermissionChange, oneof: 0

  field :completed_revision, 2,
    type: Authzed.Api.V1.ZedToken,
    json_name: "completedRevision",
    oneof: 0
end
