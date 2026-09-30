defmodule Authzed.Api.Materialize.V0.SetReference do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.SetReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :object_type, 1, type: :string, json_name: "objectType"
  field :object_id, 2, type: :string, json_name: "objectId"
  field :permission_or_relation, 3, type: :string, json_name: "permissionOrRelation"
end
