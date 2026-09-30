defmodule Authzed.Api.V1.RelationshipFilter do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.RelationshipFilter",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :resource_type, 1, type: :string, json_name: "resourceType", deprecated: false

  field :optional_resource_id, 2,
    type: :string,
    json_name: "optionalResourceId",
    deprecated: false

  field :optional_resource_id_prefix, 5,
    type: :string,
    json_name: "optionalResourceIdPrefix",
    deprecated: false

  field :optional_relation, 3, type: :string, json_name: "optionalRelation", deprecated: false

  field :optional_subject_filter, 4,
    type: Authzed.Api.V1.SubjectFilter,
    json_name: "optionalSubjectFilter"
end
