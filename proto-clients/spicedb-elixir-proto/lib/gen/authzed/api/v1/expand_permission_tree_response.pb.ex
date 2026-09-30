defmodule Authzed.Api.V1.ExpandPermissionTreeResponse do
  @moduledoc false

  use Protobuf,
    full_name: "authzed.api.v1.ExpandPermissionTreeResponse",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :expanded_at, 1, type: Authzed.Api.V1.ZedToken, json_name: "expandedAt"
  field :tree_root, 2, type: Authzed.Api.V1.PermissionRelationshipTree, json_name: "treeRoot"
end
