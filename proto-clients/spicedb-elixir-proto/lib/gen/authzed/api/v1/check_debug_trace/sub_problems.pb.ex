defmodule Authzed.Api.V1.CheckDebugTrace.SubProblems do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.CheckDebugTrace.SubProblems",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :traces, 1, repeated: true, type: Authzed.Api.V1.CheckDebugTrace
end
