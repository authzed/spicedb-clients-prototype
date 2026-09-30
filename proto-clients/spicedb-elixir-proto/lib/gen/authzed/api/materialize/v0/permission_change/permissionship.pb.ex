defmodule Authzed.Api.Materialize.V0.PermissionChange.Permissionship do
  @moduledoc false

  use Protobuf,
    enum: true,
    full_name: "authzed.api.materialize.v0.PermissionChange.Permissionship",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :PERMISSIONSHIP_UNSPECIFIED, 0
  field :PERMISSIONSHIP_NO_PERMISSION, 1
  field :PERMISSIONSHIP_HAS_PERMISSION, 2
  field :PERMISSIONSHIP_CONDITIONAL_PERMISSION, 3
end
