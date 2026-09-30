defmodule Authzed.Api.Materialize.V0.WatchedPermission do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.WatchedPermission",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :resource_type, 1, type: :string, json_name: "resourceType"
  field :permission, 2, type: :string
  field :subject_type, 3, type: :string, json_name: "subjectType"
  field :optional_subject_relation, 4, type: :string, json_name: "optionalSubjectRelation"
end
