defmodule Authzed.Api.V1.RelationshipUpdate.Operation do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.RelationshipUpdate.Operation",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :OPERATION_UNSPECIFIED, 0
  field :OPERATION_CREATE, 1
  field :OPERATION_TOUCH, 2
  field :OPERATION_DELETE, 3
end
