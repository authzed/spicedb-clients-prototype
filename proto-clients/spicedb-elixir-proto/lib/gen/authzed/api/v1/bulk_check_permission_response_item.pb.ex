defmodule Authzed.Api.V1.BulkCheckPermissionResponseItem do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.BulkCheckPermissionResponseItem",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :permissionship, 1,
    type: Authzed.Api.V1.CheckPermissionResponse.Permissionship,
    enum: true,
    deprecated: false

  field :partial_caveat_info, 2,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo",
    deprecated: false
end
