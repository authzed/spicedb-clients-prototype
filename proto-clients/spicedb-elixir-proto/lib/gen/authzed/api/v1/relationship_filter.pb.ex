defmodule Authzed.Api.V1.RelationshipFilter do
  @moduledoc """
  RelationshipFilter is a collection of filters which when applied to a
  relationship will return relationships that have exactly matching fields.

  All fields are optional and if left unspecified will not filter relationships,
  but at least one field must be specified.

  NOTE: The performance of the API will be affected by the selection of fields
  on which to filter. If a field is not indexed, the performance of the API
  can be significantly slower.
  """

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
