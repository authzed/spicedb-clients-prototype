defmodule Authzed.Api.V1.BulkCheckPermissionResponse do
  use Protobuf,
    full_name: "authzed.api.v1.BulkCheckPermissionResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :checked_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "checkedAt", deprecated: false
  field :pairs, 2, repeated: true, type: Authzed.Api.V1.BulkCheckPermissionPair, deprecated: false
end
