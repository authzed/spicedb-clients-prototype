defmodule Authzed.Api.V1.ContextualizedCaveat do
  @moduledoc """
  ContextualizedCaveat represents a reference to a caveat to be used by caveated relationships.
  The context consists of key-value pairs that will be injected at evaluation time.
  The keys must match the arguments defined on the caveat in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ContextualizedCaveat",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :caveat_name, 1, type: :string, json_name: "caveatName", deprecated: false
  field :context, 2, type: Google.Protobuf.Struct, deprecated: false
end
