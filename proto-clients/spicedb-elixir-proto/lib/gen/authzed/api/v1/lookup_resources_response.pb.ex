defmodule Authzed.Api.V1.LookupResourcesResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.LookupResourcesResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :looked_up_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "lookedUpAt"
  field :resource_object_id, 2, type: :string, json_name: "resourceObjectId"

  field :permissionship, 3,
    type: Authzed.Api.V1.LookupPermissionship,
    enum: true,
    deprecated: false

  field :partial_caveat_info, 4,
    type: Authzed.Api.V1.PartialCaveatInfo,
    json_name: "partialCaveatInfo",
    deprecated: false

  field :after_result_cursor, 5, type: Authzed.Api.V1.Cursor, json_name: "afterResultCursor"
end
