defmodule Authzed.Api.V1.Cursor do
  @moduledoc """
  Cursor is used to provide resumption of listing between calls to APIs
  such as LookupResources.
  """

  use Protobuf,
    full_name: "authzed.api.v1.Cursor",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :token, 1, type: :string, deprecated: false
end
