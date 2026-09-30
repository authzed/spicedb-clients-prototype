defmodule Authzed.Api.V1.WriteSchemaRequest do
  @moduledoc """
  WriteSchemaRequest is the required data used to "upsert" the Schema of a
  Permissions System.
  """

  use Protobuf,
    full_name: "authzed.api.v1.WriteSchemaRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :schema, 1, type: :string, deprecated: false
end
