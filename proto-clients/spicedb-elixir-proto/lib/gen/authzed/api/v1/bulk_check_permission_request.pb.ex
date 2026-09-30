defmodule Authzed.Api.V1.BulkCheckPermissionRequest do
  @moduledoc """
  NOTE: Deprecated now that BulkCheckPermission has been promoted to the stable API as "CheckBulkPermission".
  """

  use Protobuf,
    full_name: "authzed.api.v1.BulkCheckPermissionRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :items, 2,
    repeated: true,
    type: Authzed.Api.V1.BulkCheckPermissionRequestItem,
    deprecated: true
end
