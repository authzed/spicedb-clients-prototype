defmodule Authzed.Api.V1.SubjectFilter do
  @moduledoc """
  SubjectFilter specifies a filter on the subject of a relationship.

  subject_type is required and all other fields are optional, and will not
  impose any additional requirements if left unspecified.
  """

  use Protobuf,
    full_name: "authzed.api.v1.SubjectFilter",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :subject_type, 1, type: :string, json_name: "subjectType", deprecated: false
  field :optional_subject_id, 2, type: :string, json_name: "optionalSubjectId", deprecated: false

  field :optional_relation, 3,
    type: Authzed.Api.V1.SubjectFilter.RelationFilter,
    json_name: "optionalRelation"
end
