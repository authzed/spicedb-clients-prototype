defmodule Authzed.Api.V1.DeleteRelationshipsResponse.DeletionProgress do
  use Protobuf,
    enum: true,
    full_name: "authzed.api.v1.DeleteRelationshipsResponse.DeletionProgress",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :DELETION_PROGRESS_UNSPECIFIED, 0
  field :DELETION_PROGRESS_COMPLETE, 1
  field :DELETION_PROGRESS_PARTIAL, 2
end
