defmodule Authzed.Api.V1.ReflectionCaveat do
  @moduledoc """
  ReflectionCaveat is the representation of a caveat in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionCaveat",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :name, 1, type: :string
  field :comment, 2, type: :string
  field :parameters, 3, repeated: true, type: Authzed.Api.V1.ReflectionCaveatParameter
  field :expression, 4, type: :string
end
