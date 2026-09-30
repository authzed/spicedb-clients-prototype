defmodule Authzed.Api.V1.WriteSchemaResponse do
  @moduledoc """
  WriteSchemaResponse is the resulting data after having written a Schema to
  a Permissions System.
  """

  use Protobuf,
    full_name: "authzed.api.v1.WriteSchemaResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :written_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "writtenAt", deprecated: false
end
