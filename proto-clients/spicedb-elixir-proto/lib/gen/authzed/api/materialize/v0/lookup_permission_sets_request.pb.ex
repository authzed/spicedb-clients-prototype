defmodule Authzed.Api.Materialize.V0.LookupPermissionSetsRequest do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.LookupPermissionSetsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :limit, 1, type: :uint32
  field :optional_at_revision, 2, type: Authzed.Api.V1.ZedToken, json_name: "optionalAtRevision"

  field :optional_starting_after_cursor, 4,
    type: Authzed.Api.Materialize.V0.Cursor,
    json_name: "optionalStartingAfterCursor"
end
