defmodule Authzed.Api.V1.ExpCaveatParameterTypeChange do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpCaveatParameterTypeChange",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :parameter, 1, type: Authzed.Api.V1.ExpCaveatParameter
  field :previous_type, 2, type: :string, json_name: "previousType"
end
