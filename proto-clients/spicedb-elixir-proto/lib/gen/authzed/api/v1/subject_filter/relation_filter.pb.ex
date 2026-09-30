defmodule Authzed.Api.V1.SubjectFilter.RelationFilter do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.SubjectFilter.RelationFilter",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :relation, 1, type: :string, deprecated: false
end
