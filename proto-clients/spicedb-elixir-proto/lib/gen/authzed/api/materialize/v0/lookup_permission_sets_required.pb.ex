defmodule Authzed.Api.Materialize.V0.LookupPermissionSetsRequired do
  @moduledoc """
  LookupPermissionSetsRequired is a signal that the consumer should perform a LookupPermissionSets call because
  the permission set snapshot needs to be rebuilt from scratch. This typically happens when the origin SpiceDB
  cluster has seen its schema changed, see BreakingSchemaChange event.
  """

  use Protobuf,
    full_name: "authzed.api.materialize.v0.LookupPermissionSetsRequired",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :required_lookup_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "requiredLookupAt"
end
