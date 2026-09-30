defmodule Authzed.Api.V1.ExperimentalReflectSchemaRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalReflectSchemaRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :optional_filters, 2,
    repeated: true,
    type: Authzed.Api.V1.ExpSchemaFilter,
    json_name: "optionalFilters"
end
