defmodule Authzed.Api.V1.PartialCaveatInfo do
  @moduledoc """
  PartialCaveatInfo carries information necessary for the client to take action
  in the event a response contains a partially evaluated caveat
  """

  use Protobuf,
    full_name: "authzed.api.v1.PartialCaveatInfo",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :missing_required_context, 1,
    repeated: true,
    type: :string,
    json_name: "missingRequiredContext",
    deprecated: false
end
