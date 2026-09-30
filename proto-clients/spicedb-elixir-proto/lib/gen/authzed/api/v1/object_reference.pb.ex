defmodule Authzed.Api.V1.ObjectReference do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ObjectReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :object_type, 1, type: :string, json_name: "objectType", deprecated: false
  field :object_id, 2, type: :string, json_name: "objectId", deprecated: false
end
