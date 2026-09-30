defmodule Authzed.Api.Materialize.V0.DownloadPermissionSetsResponse do
  use Protobuf,
    full_name: "authzed.api.materialize.v0.DownloadPermissionSetsResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :files, 1, repeated: true, type: Authzed.Api.Materialize.V0.File
  field :timestamp, 2, type: Google.Protobuf.Timestamp
  field :at_revision, 3, type: Authzed.Api.V1.ZedToken, json_name: "atRevision"
end
