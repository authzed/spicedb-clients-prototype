defmodule Authzed.Api.Materialize.V0.LookupPermissionSetsRequired do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.LookupPermissionSetsRequired",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :required_lookup_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "requiredLookupAt"
end
