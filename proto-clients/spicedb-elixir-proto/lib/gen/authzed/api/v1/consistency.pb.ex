defmodule Authzed.Api.V1.Consistency do
  @moduledoc """
  Consistency will define how a request is handled by the backend.
  By defining a consistency requirement, and a token at which those
  requirements should be applied, where applicable.
  """

  use Protobuf,
    full_name: "authzed.api.v1.Consistency",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :requirement, 0

  field :minimize_latency, 1,
    type: :bool,
    json_name: "minimizeLatency",
    oneof: 0,
    deprecated: false

  field :at_least_as_fresh, 2,
    type: Authzed.Api.V1.ZedToken,
    json_name: "atLeastAsFresh",
    oneof: 0

  field :at_exact_snapshot, 3,
    type: Authzed.Api.V1.ZedToken,
    json_name: "atExactSnapshot",
    oneof: 0

  field :fully_consistent, 4,
    type: :bool,
    json_name: "fullyConsistent",
    oneof: 0,
    deprecated: false
end
