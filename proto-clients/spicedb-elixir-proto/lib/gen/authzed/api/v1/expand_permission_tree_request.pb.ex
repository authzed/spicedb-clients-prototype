defmodule Authzed.Api.V1.ExpandPermissionTreeRequest do
  @moduledoc """
  ExpandPermissionTreeRequest returns a tree representing the expansion of all
  relationships found accessible from a permission or relation on a particular
  resource.

  ExpandPermissionTreeRequest is typically used to determine the full set of
  subjects with a permission, along with the relationships that grant said
  access.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ExpandPermissionTreeRequest",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :consistency, 1, type: Authzed.Api.V1.Consistency
  field :resource, 2, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :permission, 3, type: :string, deprecated: false
end
