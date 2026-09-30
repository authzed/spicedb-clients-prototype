defmodule Authzed.Api.Materialize.V0.PermissionSetChange do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.PermissionSetChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :child, 0

  field :at_revision, 1, type: Authzed.Api.V1.ZedToken, json_name: "atRevision"

  field :operation, 2,
    type: Authzed.Api.Materialize.V0.PermissionSetChange.SetOperation,
    enum: true

  field :parent_set, 3, type: Authzed.Api.Materialize.V0.SetReference, json_name: "parentSet"

  field :child_set, 4,
    type: Authzed.Api.Materialize.V0.SetReference,
    json_name: "childSet",
    oneof: 0

  field :child_member, 5,
    type: Authzed.Api.Materialize.V0.MemberReference,
    json_name: "childMember",
    oneof: 0
end
