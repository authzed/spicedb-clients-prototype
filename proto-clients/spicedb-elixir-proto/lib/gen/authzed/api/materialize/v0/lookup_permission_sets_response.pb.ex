defmodule Authzed.Api.Materialize.V0.LookupPermissionSetsResponse do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.LookupPermissionSetsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :change, 1, type: Authzed.Api.Materialize.V0.PermissionSetChange
  field :cursor, 2, type: Authzed.Api.Materialize.V0.Cursor
end
