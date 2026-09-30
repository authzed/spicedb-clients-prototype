defmodule Authzed.Api.Materialize.V0.PermissionSetChange.SetOperation do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.materialize.v0.PermissionSetChange.SetOperation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :SET_OPERATION_UNSPECIFIED, 0
  field :SET_OPERATION_ADDED, 1
  field :SET_OPERATION_REMOVED, 2
end
