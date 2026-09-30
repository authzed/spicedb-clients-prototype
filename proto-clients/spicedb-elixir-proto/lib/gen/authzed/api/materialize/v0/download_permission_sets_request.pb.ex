defmodule Authzed.Api.Materialize.V0.DownloadPermissionSetsRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.materialize.v0.DownloadPermissionSetsRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :optional_at_revision, 1, type: Authzed.Api.V1.ZedToken, json_name: "optionalAtRevision"
end
