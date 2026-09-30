defmodule Authzed.Api.V1.LookupPermissionship do
  @moduledoc false

  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.LookupPermissionship",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :LOOKUP_PERMISSIONSHIP_UNSPECIFIED, 0
  field :LOOKUP_PERMISSIONSHIP_HAS_PERMISSION, 1
  field :LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION, 2
end
