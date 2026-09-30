defmodule Authzed.Api.Materialize.V0.WatchPermissionSetsResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.WatchPermissionSetsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :response, 0

  field :change, 1, type: Authzed.Api.Materialize.V0.PermissionSetChange, oneof: 0

  field :completed_revision, 2,
    type: Authzed.Api.V1.ZedToken,
    json_name: "completedRevision",
    oneof: 0

  field :lookup_permission_sets_required, 3,
    type: Authzed.Api.Materialize.V0.LookupPermissionSetsRequired,
    json_name: "lookupPermissionSetsRequired",
    oneof: 0

  field :breaking_schema_change, 4,
    type: Authzed.Api.Materialize.V0.BreakingSchemaChange,
    json_name: "breakingSchemaChange",
    oneof: 0
end
