defmodule Authzed.Api.V1.CheckBulkPermissionsRequest do
  @moduledoc """
  CheckBulkPermissionsRequest issues a check on whether a subject has permission
  or is a member of a relation on a specific resource for each item in the list.

  The ordering of the items in the response is maintained in the response.
  Checks with the same subject/permission will automatically be batched for performance optimization.
  """

  use Protobuf,
    full_name: "authzed.api.v1.CheckBulkPermissionsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency

  field :items, 2,
    repeated: true,
    type: Authzed.Api.V1.CheckBulkPermissionsRequestItem,
    deprecated: false

  field :with_tracing, 3, type: :bool, json_name: "withTracing"
end
