defmodule Authzed.Api.V1.CheckBulkPermissionsPair do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.CheckBulkPermissionsPair",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :response, 0

  field :request, 1, type: Authzed.Api.V1.CheckBulkPermissionsRequestItem
  field :item, 2, type: Authzed.Api.V1.CheckBulkPermissionsResponseItem, oneof: 0
  field :error, 3, type: Google.Rpc.Status, oneof: 0
end
