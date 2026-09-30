defmodule Authzed.Api.V1.CheckBulkPermissionsRequest do
  @moduledoc false

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
