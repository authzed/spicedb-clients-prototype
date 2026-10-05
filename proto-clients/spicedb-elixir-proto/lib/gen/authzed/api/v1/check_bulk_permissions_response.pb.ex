defmodule Authzed.Api.V1.CheckBulkPermissionsResponse do
  use Protobuf,
    full_name: "authzed.api.v1.CheckBulkPermissionsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :checked_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "checkedAt", deprecated: false

  field :pairs, 2,
    repeated: true,
    type: Authzed.Api.V1.CheckBulkPermissionsPair,
    deprecated: false
end
