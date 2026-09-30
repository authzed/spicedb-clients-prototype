defmodule Authzed.Api.V1.ReflectionCaveatParameterTypeChange do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionCaveatParameterTypeChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :parameter, 1, type: Authzed.Api.V1.ReflectionCaveatParameter
  field :previous_type, 2, type: :string, json_name: "previousType"
end
