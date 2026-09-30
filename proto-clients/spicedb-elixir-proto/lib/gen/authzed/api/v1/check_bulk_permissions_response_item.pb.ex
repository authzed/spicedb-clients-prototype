defmodule Authzed.Api.V1.CheckBulkPermissionsResponseItem do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.CheckBulkPermissionsResponseItem",
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

  field :debug_trace, 3, type: Authzed.Api.V1.DebugInformation, json_name: "debugTrace"
end
