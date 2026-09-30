defmodule Authzed.Api.V1.CheckPermissionResponse do
  use Protobuf,
    full_name: "authzed.api.v1.CheckPermissionResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :checked_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "checkedAt", deprecated: false

  field :permissionship, 2,
    type: Authzed.Api.V1.CheckPermissionResponse.Permissionship,
    enum: true,
    deprecated: false

  field :partial_caveat_info, 3,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo",
    deprecated: false

  field :debug_trace, 4, type: Authzed.Api.V1.DebugInformation, json_name: "debugTrace"
  field :optional_expires_at, 5, type: Google.Protobuf.Timestamp, json_name: "optionalExpiresAt"
end
