defmodule Authzed.Api.Materialize.V0.PermissionChange do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.PermissionChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :revision, 1, type: Authzed.Api.V1.ZedToken
  field :resource, 2, type: Authzed.Api.V1.ObjectReference
  field :permission, 3, type: :string
  field :subject, 4, type: Authzed.Api.V1.SubjectReference

  field :permissionship, 5,
    type: Authzed.Api.Materialize.V0.PermissionChange.Permissionship,
    enum: true
end
