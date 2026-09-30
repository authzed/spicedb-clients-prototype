defmodule Authzed.Api.V1.DebugInformation do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.DebugInformation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :check, 1, type: Authzed.Api.V1.CheckDebugTrace
  field :schema_used, 2, type: :string, json_name: "schemaUsed"
end
