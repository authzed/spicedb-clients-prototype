defmodule Authzed.Api.V1.DebugInformation do
  @moduledoc """
  DebugInformation defines debug information returned by an API call in a footer when
  requested with a specific debugging header.

  The specific debug information returned will depend on the type of the API call made.

  See the github.com/authzed/authzed-go project for the specific header and footer names.
  """

  use Protobuf,
    full_name: "authzed.api.v1.DebugInformation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :check, 1, type: Authzed.Api.V1.CheckDebugTrace
  field :schema_used, 2, type: :string, json_name: "schemaUsed"
end
