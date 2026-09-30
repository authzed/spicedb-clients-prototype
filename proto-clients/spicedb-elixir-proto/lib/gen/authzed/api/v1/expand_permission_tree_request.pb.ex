defmodule Authzed.Api.V1.ExpandPermissionTreeRequest do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpandPermissionTreeRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :resource, 2, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 3, type: :string, deprecated: false
end
