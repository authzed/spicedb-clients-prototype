defmodule Authzed.Api.V1.AlgebraicSubjectSet.Operation do
  @moduledoc false

  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.AlgebraicSubjectSet.Operation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :OPERATION_UNSPECIFIED, 0
  field :OPERATION_UNION, 1
  field :OPERATION_INTERSECTION, 2
  field :OPERATION_EXCLUSION, 3
end
