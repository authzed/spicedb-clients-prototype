defmodule Authzed.Api.V1.ReflectionTypeReference do
  @moduledoc """
  ReflectionTypeReference is the representation of a type reference in the schema.
  """

  use Protobuf,
    full_name: "authzed.api.v1.ReflectionTypeReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  oneof :typeref, 0

  field :subject_definition_name, 1, type: :string, json_name: "subjectDefinitionName"
  field :optional_caveat_name, 2, type: :string, json_name: "optionalCaveatName"
  field :is_terminal_subject, 3, type: :bool, json_name: "isTerminalSubject", oneof: 0
  field :optional_relation_name, 4, type: :string, json_name: "optionalRelationName", oneof: 0
  field :is_public_wildcard, 5, type: :bool, json_name: "isPublicWildcard", oneof: 0
end
