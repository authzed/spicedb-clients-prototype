defmodule Authzed.Api.V1.PermissionRelationshipTree do
  @moduledoc """
  PermissionRelationshipTree is used for representing a tree of a resource and
  its permission relationships with other objects.
  """

  use Protobuf,
    full_name: "authzed.api.v1.PermissionRelationshipTree",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :tree_type, 0

  field :intermediate, 1, type: Authzed.Api.V1.AlgebraicSubjectSet, oneof: 0
  field :leaf, 2, type: Authzed.Api.V1.DirectSubjectSet, oneof: 0
  field :expanded_object, 3, type: Authzed.Api.V1.ObjectReference, json_name: "expandedObject"
  field :expanded_relation, 4, type: :string, json_name: "expandedRelation"
end
