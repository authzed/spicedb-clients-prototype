defmodule Authzed.Api.V1.CaveatEvalInfo.Result do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.CaveatEvalInfo.Result",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :RESULT_UNSPECIFIED, 0
  field :RESULT_UNEVALUATED, 1
  field :RESULT_FALSE, 2
  field :RESULT_TRUE, 3
  field :RESULT_MISSING_SOME_CONTEXT, 4
end
