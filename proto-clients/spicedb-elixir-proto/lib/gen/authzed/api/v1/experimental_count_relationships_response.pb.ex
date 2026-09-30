defmodule Authzed.Api.V1.ExperimentalCountRelationshipsResponse do
  use Protobuf,
    full_name: "authzed.api.v1.ExperimentalCountRelationshipsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :counter_result, 0

  field :counter_still_calculating, 1, type: :bool, json_name: "counterStillCalculating", oneof: 0

  field :read_counter_value, 2,
    type: Authzed.Api.V1.ReadCounterValue,
    json_name: "readCounterValue",
    oneof: 0
end
