defmodule Authzed.Api.V1.ExpCaveatParameter do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpCaveatParameter",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :type, 2, type: :string
  field :parent_caveat_name, 3, type: :string, json_name: "parentCaveatName"
end
