defmodule Authzed.Api.V1.CaveatEvalInfo do
  @moduledoc """
  CaveatEvalInfo holds information about a caveat expression that was evaluated.
  """

  use Protobuf,
    full_name: "authzed.api.v1.CaveatEvalInfo",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :expression, 1, type: :string
  field :result, 2, type: Authzed.Api.V1.CaveatEvalInfo.Result, enum: true
  field :context, 3, type: Google.Protobuf.Struct

  field :partial_caveat_info, 4,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo"

  field :caveat_name, 5, type: :string, json_name: "caveatName"
end
