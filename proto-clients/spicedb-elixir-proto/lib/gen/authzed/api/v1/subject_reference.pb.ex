defmodule Authzed.Api.V1.SubjectReference do
  @moduledoc """
  SubjectReference is used for referring to the subject portion of a
  Relationship. The relation component is optional and is used for defining a
  sub-relation on the subject, e.g. group:123#members
  """

  use Protobuf,
    full_name: "authzed.api.v1.SubjectReference",
    protoc_gen_elixir_version: "0.17.0",
    syntax: :proto3

  field :object, 1, type: Authzed.Api.V1.ObjectReference, deprecated: false
  field :optional_relation, 2, type: :string, json_name: "optionalRelation", deprecated: false
end
