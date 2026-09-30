defmodule Authzed.Api.V1.ReflectionSchemaFilter do
  @moduledoc """
  ReflectionSchemaFilter is a filter that can be applied to the schema on reflection.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionSchemaFilter",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :optional_definition_name_filter, 1,
    type: :string,
    json_name: "optionalDefinitionNameFilter"

  field :optional_caveat_name_filter, 2, type: :string, json_name: "optionalCaveatNameFilter"
  field :optional_relation_name_filter, 3, type: :string, json_name: "optionalRelationNameFilter"

  field :optional_permission_name_filter, 4,
    type: :string,
    json_name: "optionalPermissionNameFilter"
end
