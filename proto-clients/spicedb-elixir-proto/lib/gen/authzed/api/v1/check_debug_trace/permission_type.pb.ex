defmodule Authzed.Api.V1.CheckDebugTrace.PermissionType do
  @moduledoc false

  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.CheckDebugTrace.PermissionType",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :PERMISSION_TYPE_UNSPECIFIED, 0
  field :PERMISSION_TYPE_RELATION, 1
  field :PERMISSION_TYPE_PERMISSION, 2
end
