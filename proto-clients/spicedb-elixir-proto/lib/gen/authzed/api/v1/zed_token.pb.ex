defmodule Authzed.Api.V1.ZedToken do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ZedToken",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :token, 1, type: :string, deprecated: false
end
