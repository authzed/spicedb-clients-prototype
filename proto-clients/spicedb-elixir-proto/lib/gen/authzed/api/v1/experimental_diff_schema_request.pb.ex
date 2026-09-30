defmodule Authzed.Api.V1.ExperimentalDiffSchemaRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalDiffSchemaRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :comparison_schema, 2, type: :string, json_name: "comparisonSchema"
end
