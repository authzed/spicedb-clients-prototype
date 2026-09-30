defmodule Authzed.Api.V1.ReadSchemaRequest do
  @moduledoc """
  ReadSchemaRequest returns the schema from the database.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReadSchemaRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3
end
