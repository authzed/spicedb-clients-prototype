defmodule Authzed.Api.V1.Precondition.Operation do
  @moduledoc false

  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.Precondition.Operation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :OPERATION_UNSPECIFIED, 0
  field :OPERATION_MUST_NOT_MATCH, 1
  field :OPERATION_MUST_MATCH, 2
end
