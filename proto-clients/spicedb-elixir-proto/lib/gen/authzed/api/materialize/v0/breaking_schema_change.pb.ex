defmodule Authzed.Api.Materialize.V0.BreakingSchemaChange do
  @moduledoc """
  BreakingSchemaChange is used to signal a breaking schema change has happened, and that the consumer should
  expect delays in the ingestion of new changes, because the permission set snapshot needs to be rebuilt from scratch.
  Once the snapshot is ready, the consumer will receive a LookupPermissionSetsRequired event.
  """

  use Protobuf,
    full_name: "authzed.api.materialize.v0.BreakingSchemaChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :change_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "changeAt"

  field :affected_permissions, 2,
    repeated: true,
    type: Authzed.Api.Materialize.V0.WatchedPermission,
    json_name: "affectedPermissions"
end
