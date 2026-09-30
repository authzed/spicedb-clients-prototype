defmodule Authzed.Api.V1.ReflectionRelationSubjectTypeChange do
  use Protobuf,
    full_name: "authzed.api.v1.ReflectionRelationSubjectTypeChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relation, 1, type: Authzed.Api.V1.ReflectionRelation

  field :changed_subject_type, 2,
    type: Authzed.Api.V1.ReflectionTypeReference,
    json_name: "changedSubjectType"
end
