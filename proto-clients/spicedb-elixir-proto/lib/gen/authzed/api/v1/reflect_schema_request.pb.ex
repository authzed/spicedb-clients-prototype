defmodule Authzed.Api.V1.ReflectSchemaRequest do
  @moduledoc """
  Reflection types ////////////////////////////////////////////
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReflectSchemaRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :optional_filters, 2,
    repeated: true,
    type: Authzed.Api.V1.ReflectionSchemaFilter,
    json_name: "optionalFilters"
end
