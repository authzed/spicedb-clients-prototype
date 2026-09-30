defmodule Authzed.Api.V1.ContextualizedCaveat do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ContextualizedCaveat",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :caveat_name, 1, type: :string, json_name: "caveatName", deprecated: false
  field :context, 2, type: Google.Protobuf.Struct, deprecated: false
end
