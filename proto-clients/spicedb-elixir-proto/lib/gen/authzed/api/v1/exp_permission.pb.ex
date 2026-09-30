defmodule Authzed.Api.V1.ExpPermission do
  @moduledoc """
  ExpPermission is the representation of a permission in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ExpPermission",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :comment, 2, type: :string
  field :parent_definition_name, 3, type: :string, json_name: "parentDefinitionName"
end
