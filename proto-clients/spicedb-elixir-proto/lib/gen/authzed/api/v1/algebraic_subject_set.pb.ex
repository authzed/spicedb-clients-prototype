defmodule Authzed.Api.V1.AlgebraicSubjectSet do
  @moduledoc """
  AlgebraicSubjectSet is a subject set which is computed based on applying the
  specified operation to the operands according to the algebra of sets.

  UNION is a logical set containing the subject members from all operands.

  INTERSECTION is a logical set containing only the subject members which are
  present in all operands.

  EXCLUSION is a logical set containing only the subject members which are
  present in the first operand, and none of the other operands.
  """

  use Protobuf,
    full_name: "authzed.api.v1.AlgebraicSubjectSet",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :operation, 1,
    type: Authzed.Api.V1.AlgebraicSubjectSet.Operation,
    enum: true,
    deprecated: false

  field :children, 2,
    repeated: true,
    type: Authzed.Api.V1.PermissionRelationshipTree,
    deprecated: false
end
